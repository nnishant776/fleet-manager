# Test that a fleet sync which stops on a problem tells every account, and
# that it does not take the machine back to an older commit, on a host built
# from this flake's modules (tests/fleet/host) whose upstream is a local
# repository. The checks:
# - an unsigned commit stops the sync and is recorded in
#   /var/lib/fleet-sync/status, readable by everyone, with the commit;
# - an unprivileged user sees the notice at login, and `fleet-sync-status`
#   prints it;
# - users named in /var/lib/fleet-sync/user-problems (whose own secrets the
#   sync could not take) are shown at login and by `fleet-sync-status`, with
#   or without a stopped sync;
# - the same problem on the next run keeps its first time;
# - a different problem (a signed commit that does not build) replaces it;
# - a signed commit that builds is applied, and recorded, readable by
#   everyone, in /var/lib/fleet-sync/accepted as the last commit this machine
#   accepted; the commit that did not build was recorded too;
# - a branch taken back to an older signed commit is refused, with the
#   checkout and the record left as they were;
# - removing the record lets the sync accept that older commit;
# - a record of the same history under another address passes as it is;
# - a record from another history, as on a host whose configuration has just
#   moved, stops the sync until it is removed, which lets the sync start
#   from the newest commit there;
# - after a sync the checkout is the verified commit and nothing else: HEAD
#   detached at it, origin the only remote, holding only the tracked branch,
#   and no local branches;
# - a privileged account with no password starts the sync at once with
#   `fleet-sync-now`, which shows the log and the result; an unprivileged one
#   cannot, and the rule allows nothing else;
# - a missing checkout stops the sync, which starts the first-boot clone;
# - a run that finds nothing new repairs support's access: its keys file, its
#   account, /etc/sudoers, /etc/ssh/sshd_config and sshd, whichever was broken.
# The graphical notification is not covered: the VM has no session.
# Run it with `nix build .#checks.x86_64-linux.fleet-sync-status`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "checkout"; hostDir = ./host; };

  signer = pkgs.runCommand "fleet-sync-status-signer" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    ssh-keygen -q -t ed25519 -N "" -C signer -f $out/signer
    echo "signer namespaces=\"git\" $(cut -d' ' -f1,2 $out/signer.pub)" > $out/allowed_signers
  '';

  git = lib.getExe pkgs.git;
  # Runs git as root in the upstream work tree, signing with the allowed key
  # when the first argument is "signed".
  upstream = pkgs.writeShellScript "upstream" ''
    set -eu
    cd /srv/work
    sign=""
    if [ "$1" = signed ]; then
      install -m 0600 ${signer}/signer /root/signer
      sign="-c gpg.format=ssh -c user.signingKey=/root/signer -c commit.gpgSign=true"
    fi
    shift
    # shellcheck disable=SC2086 # the -c options are meant to split
    ${git} -c user.name=root -c user.email=root@localhost $sign "$@"
  '';

  # Stands in for a configuration that builds, since the VM cannot evaluate a
  # real one: a flake with no inputs, so it needs no network, whose system is
  # a directory with the nixos-version file nixos-rebuild looks for and a
  # switch-to-configuration that does nothing. The sync builds it and
  # nixos-rebuild switches to it as they would for a real one, and the
  # machine stays as it is. nixos-rebuild first builds the new
  # configuration's own nixos-rebuild to run in its place; this one has none
  # in it, so nixos-rebuild carries on as itself.
  buildable = pkgs.writeText "flake.nix" ''
    {
      outputs = _: {
        nixosConfigurations.checkout.config.system.build = {
          toplevel = ./system;
          nixos-rebuild = ./system;
        };
      };
    }
  '';
  noSwitch = pkgs.writeScript "switch-to-configuration" ''
    #!/bin/sh
    exit 0
  '';
  # A configuration that builds and stands for the running system: for `test`
  # it activates the real system (whose path the test puts in /srv/real-system)
  # again, as a real switch-to-configuration would the running one, and it
  # leaves /run/current-system pointing at itself, so that a sync with nothing
  # new finds it already running, as on a real host.
  standIn = pkgs.writeScript "switch-to-configuration" ''
    #!/bin/sh
    if [ "$1" = test ]; then
      "$(${pkgs.coreutils}/bin/cat /srv/real-system)/bin/switch-to-configuration" test || exit
    fi
    c=${pkgs.coreutils}/bin
    "$c/ln" -sfn "$("$c/dirname" "$("$c/dirname" "$("$c/readlink" -f "$0")")")" /run/current-system
  '';
in
pkgs.testers.runNixOSTest {
  name = "fleet-sync-status";

  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.checkout = {
    imports = hostNode.modules ++ [{
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      conf.fleet = {
        repo.url = "file:///srv/upstream.git";
        signing.allowedSignersFile = "${signer}/allowed_signers";
        # A key from the configuration, since this node has no secrets.
        support = {
          enable = true;
          sshKeys = [ "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHN1cHBvcnRLZXlGb3JUaGVTeW5jVGVzdE9ubHk0000 support@test" ];
        };
      };
      # A privileged account with no password, as on a fleet host where sudo
      # works only by a forwarded SSH agent.
      conf.users.accounts.ann = { fullName = "Ann"; privileged = true; };

      # The test makes the checkout and runs the sync itself.
      systemd.services.fleet-bootstrap.enable = false;
      systemd.timers.config-auto-update.enable = false;
    }];
  };

  testScript = ''
    checkout.wait_for_unit("multi-user.target")

    def sync():
        checkout.execute("systemctl start config-auto-update.service")

    def field(name):
        return checkout.succeed(f"sed -n 's/^{name}=//p' /var/lib/fleet-sync/status").strip()

    def accepted():
        return checkout.succeed("cat /var/lib/fleet-sync/accepted").strip()

    def head(repo):
        return checkout.succeed(f"${git} -C {repo} rev-parse HEAD").strip()

    # Upstream with one signed commit, and the root-owned checkout from it.
    checkout.succeed(
        "${git} init -q --bare -b main /srv/upstream.git",
        "${git} clone -q /srv/upstream.git /srv/work",
        "echo one > /srv/work/file",
        "${upstream} signed add file",
        "${upstream} signed commit -q -m one",
        "${upstream} signed push -q origin HEAD:main",
        "${git} clone -q -b main file:///srv/upstream.git /etc/nixos",
    )

    with subtest("nothing is recorded while nothing is wrong"):
        checkout.fail("test -e /var/lib/fleet-sync/status")
        out = checkout.succeed("su -l bob -c true 2>&1")
        assert "have stopped" not in out, out

    with subtest("users whose own secrets could not be updated are named, without stopping updates"):
        # As the sync leaves them: one line per user, a user name and a reason.
        checkout.succeed(
            "install -d -m 755 /var/lib/fleet-sync",
            "printf 'dave commit abc is not signed\\nerin has no branch user/erin\\n' > /var/lib/fleet-sync/user-problems",
            "chmod 644 /var/lib/fleet-sync/user-problems",
        )
        out = checkout.succeed("su -l bob -c true 2>&1")
        assert "Some users' secrets on this machine have not updated" in out, out
        assert "dave: commit abc is not signed" in out, out
        assert "erin: has no branch user/erin" in out, out
        assert "Please report this to your administrator." in out, out
        assert "have stopped" not in out, out
        out = checkout.succeed("runuser -u bob -- fleet-sync-status")
        assert "dave: commit abc is not signed" in out, out
        # Alongside a stopped sync, both are told.
        checkout.succeed(
            "printf 'kind=build\\ncommit=\\nsince=2026-01-01T00:00:00+00:00\\nmessage=It does not build.\\n' > /var/lib/fleet-sync/status",
        )
        out = checkout.succeed("runuser -u bob -- fleet-sync-status")
        assert "It does not build." in out and "dave: commit abc is not signed" in out, out
        out = checkout.succeed("su -l bob -c true 2>&1")
        assert "Configuration updates on this machine have stopped" in out, out
        checkout.succeed("rm /var/lib/fleet-sync/status /var/lib/fleet-sync/user-problems")
        out = checkout.succeed("runuser -u bob -- fleet-sync-status")
        assert "Configuration updates are working." in out, out

    with subtest("an unsigned commit stops the sync and is recorded for everyone"):
        checkout.succeed(
            "echo two > /srv/work/file",
            "${upstream} unsigned commit -q -am two",
            "${upstream} unsigned push -q origin HEAD:main",
        )
        unsigned = checkout.succeed("${git} -C /srv/work rev-parse HEAD").strip()
        sync()
        assert field("kind") == "signature", field("kind")
        assert field("commit") == unsigned, field("commit")
        checkout.succeed("test \"$(stat -c %a /var/lib/fleet-sync)\" = 755")
        checkout.succeed("test \"$(stat -c %a /var/lib/fleet-sync/status)\" = 644")

    with subtest("an unprivileged user sees it at login and on demand"):
        out = checkout.succeed("su -l bob -c true 2>&1")
        assert "Configuration updates on this machine have stopped" in out, out
        assert "not signed by a trusted key" in out, out
        assert "Please report this to your administrator." in out, out
        assert unsigned[:12] in out, out
        out = checkout.succeed("runuser -u bob -- fleet-sync-status")
        assert "not signed by a trusted key" in out, out

    with subtest("the same problem on the next run keeps its first time"):
        first = field("since")
        checkout.succeed("sleep 2")
        sync()
        assert field("since") == first, (field("since"), first)

    with subtest("a different problem replaces it"):
        # Signed, so the sync accepts it, but the repository holds no flake,
        # so the build fails.
        checkout.succeed(
            "echo three > /srv/work/file",
            "${upstream} signed commit -q -am three",
            "${upstream} signed push -q origin HEAD:main",
        )
        sync()
        assert field("kind") == "build", field("kind")
        out = checkout.succeed("su -l bob -c true 2>&1")
        assert "does not build" in out, out
        assert "not signed" not in out, out

    with subtest("a signed commit that builds is applied and recorded"):
        # The commit that does not build was verified and checked out, so it
        # is recorded; the fix goes on top of it.
        assert accepted() == head("/srv/work"), accepted()
        checkout.succeed(
            "cp --no-preserve=mode ${buildable} /srv/work/flake.nix",
            "install -D -m 0755 ${noSwitch} /srv/work/system/bin/switch-to-configuration",
            "echo stand-in > /srv/work/system/nixos-version",
            "${upstream} signed add flake.nix system",
            "${upstream} signed commit -q -m four",
            "${upstream} signed push -q origin HEAD:main",
        )
        four = head("/srv/work")
        checkout.succeed("systemctl start config-auto-update.service")
        assert accepted() == four, accepted()
        checkout.succeed("test \"$(stat -c %a /var/lib/fleet-sync/accepted)\" = 644")
        checkout.fail("test -e /var/lib/fleet-sync/status")

    with subtest("a rollback to an older signed commit is refused"):
        # An empty commit builds the same system. Once it is applied, the
        # branch is forced back to the commit before it.
        checkout.succeed(
            "${upstream} signed commit -q --allow-empty -m five",
            "${upstream} signed push -q origin HEAD:main",
            "systemctl start config-auto-update.service",
        )
        five = head("/srv/work")
        assert accepted() == five, accepted()
        checkout.succeed(f"${upstream} unsigned push -q --force origin {four}:refs/heads/main")
        checkout.fail("systemctl start config-auto-update.service")
        assert field("kind") == "rollback", field("kind")
        assert field("commit") == four, field("commit")
        assert head("/etc/nixos") == five, head("/etc/nixos")
        assert accepted() == five, accepted()

    with subtest("removing the record accepts it"):
        checkout.succeed(
            "rm /var/lib/fleet-sync/accepted",
            "systemctl start config-auto-update.service",
        )
        assert accepted() == four, accepted()
        assert head("/etc/nixos") == four, head("/etc/nixos")
        checkout.fail("test -e /var/lib/fleet-sync/status")

    url = "file:///srv/upstream.git"

    with subtest("a new address for the same history passes as it is"):
        assert checkout.succeed("cat /var/lib/fleet-sync/accepted.url").strip() == url
        checkout.succeed(
            "echo file:///srv/renamed.git > /var/lib/fleet-sync/accepted.url",
            "systemctl start config-auto-update.service",
        )
        assert accepted() == four, accepted()
        assert checkout.succeed("cat /var/lib/fleet-sync/accepted.url").strip() == url
        checkout.fail("test -e /var/lib/fleet-sync/status")

    with subtest("a move to another history stops until the record is removed"):
        # As on a host whose configuration has just moved to this repository:
        # the record names a commit of the old one, which four does not follow.
        # Taking whatever the new one serves would let anyone who can push
        # there serve an old commit of it.
        checkout.succeed(
            f"echo {five} > /var/lib/fleet-sync/accepted",
            "echo file:///srv/old.git > /var/lib/fleet-sync/accepted.url",
        )
        checkout.fail("systemctl start config-auto-update.service")
        assert field("kind") == "moved", field("kind")
        assert accepted() == five, accepted()
        checkout.succeed(
            "rm /var/lib/fleet-sync/accepted",
            "systemctl start config-auto-update.service",
        )
        assert accepted() == four, accepted()
        assert checkout.succeed("cat /var/lib/fleet-sync/accepted.url").strip() == url
        checkout.fail("test -e /var/lib/fleet-sync/status")

    with subtest("the checkout holds the verified commit and nothing else"):
        # What a machine set up by hand, or moved from another repository,
        # leaves behind: a branch of its own checked out, another remote, and
        # refs of branches the sync does not track.
        checkout.succeed(
            "${git} -C /etc/nixos checkout -q -b sudo-ssh-key",
            "${git} -C /etc/nixos branch -q leftover",
            "${git} -C /etc/nixos remote add before-fleet file:///srv/old.git",
            f"${git} -C /etc/nixos update-ref refs/remotes/before-fleet/main {four}",
            f"${git} -C /etc/nixos update-ref refs/remotes/origin/stale {four}",
            "${git} -C /etc/nixos symbolic-ref refs/remotes/origin/HEAD refs/remotes/origin/main",
            "systemctl start config-auto-update.service",
        )
        checkout.fail("${git} -C /etc/nixos symbolic-ref -q HEAD")
        assert head("/etc/nixos") == four, head("/etc/nixos")
        refs = checkout.succeed("${git} -C /etc/nixos for-each-ref --format='%(refname)'").split()
        assert refs == ["refs/remotes/origin/main"], refs
        remotes = checkout.succeed("${git} -C /etc/nixos remote").split()
        assert remotes == ["origin"], remotes
        checkout.fail("test -e /var/lib/fleet-sync/status")

    # The unit's runs so far, by the line systemd writes at each start: an
    # inactive oneshot unit does not keep its InvocationID.
    def invocation():
        return checkout.execute("journalctl -u config-auto-update -o cat | grep -c '^Starting Sync'")[1].strip()

    with subtest("a privileged account starts the sync without a password"):
        checkout.succeed("${upstream} signed commit -q --allow-empty -m six")
        checkout.succeed("${upstream} signed push -q origin HEAD:main")
        six = head("/srv/work")
        before = invocation()
        # A machine switched to this commit would run it; the stand-in
        # system does not stamp one, so say so here.
        checkout.succeed(f"rm -f /etc/org/revision; echo {six} > /etc/org/revision")
        out = checkout.succeed("su - ann -c fleet-sync-now </dev/null 2>&1")
        print(out)
        assert head("/etc/nixos") == six, head("/etc/nixos")
        assert invocation() != before
        assert "config-auto-update: done at " + six in out, out
        assert "the newest commit of main" in out, out
        assert "Configuration updates are working." in out, out

    with subtest("an unprivileged account cannot"):
        before = invocation()
        status, out = checkout.execute("runuser -u bob -- fleet-sync-now 2>&1")
        assert status == 1, (status, out)
        assert "only privileged accounts can start the sync" in out, out
        assert invocation() == before
        checkout.fail("runuser -u ann -- /run/wrappers/bin/sudo -n systemctl start config-auto-update")
        assert invocation() == before

    with subtest("a missing checkout stops the sync, which starts the first-boot clone"):
        # This machine has the first-boot clone switched off, so starting it
        # fails; the sync says so and stops as it should all the same.
        checkout.succeed("mv /etc/nixos /etc/nixos.away")
        checkout.fail("systemctl start config-auto-update.service")
        assert field("kind") == "checkout", field("kind")
        checkout.succeed("journalctl -u config-auto-update | grep -q 'starting the first-boot clone'")
        checkout.succeed(
            "mv /etc/nixos.away /etc/nixos",
            "systemctl start config-auto-update.service",
        )
        checkout.fail("test -e /var/lib/fleet-sync/status")

    # Applied once with the stand-in, which then counts as the running system.
    checkout.succeed(
        "readlink -f /run/current-system > /srv/real-system",
        "sed 's|./system|./system2|g' ${buildable} > /srv/work/flake.nix",
        "install -D -m 0755 ${standIn} /srv/work/system2/bin/switch-to-configuration",
        "echo stand-in > /srv/work/system2/nixos-version",
        # The test driver finds its tools under /run/current-system/sw.
        "ln -s \"$(cat /srv/real-system)/sw\" /srv/work/system2/sw",
        "${upstream} signed add flake.nix system2",
        "${upstream} signed commit -q -m seven",
        "${upstream} signed push -q origin HEAD:main",
        "systemctl start config-auto-update.service",
    )
    checkout.fail("test -e /var/lib/fleet-sync/status")

    def repairs():
        return int(checkout.execute("journalctl -u config-auto-update | grep -c 'repaired support access'")[1].strip())

    def repaired(breaking):
        before = repairs()
        checkout.succeed("systemctl start config-auto-update.service")  # nothing new
        assert repairs() == before, "support was intact: nothing to repair"
        checkout.succeed(breaking)
        checkout.succeed("systemctl start config-auto-update.service")
        assert repairs() == before + 1, (repairs(), before)
        checkout.fail("test -e /var/lib/fleet-sync/status")

    keys = "/etc/ssh/authorized_keys.d/support"

    with subtest("a sync with nothing new leaves intact support access alone"):
        checkout.succeed("systemctl start config-auto-update.service")
        checkout.succeed("journalctl -u config-auto-update | grep -q 'already running the configuration'")
        assert repairs() == 0, repairs()
        checkout.succeed(f"test -s {keys}")

    with subtest("a removed keys file is put back"):
        repaired(f"rm -f {keys}")
        checkout.succeed(f"test -s {keys}")

    with subtest("a deleted support account is made again"):
        repaired("userdel support")
        checkout.succeed("getent passwd support")

    with subtest("an edited sudoers is restored"):
        repaired("chmod u+w /etc/sudoers; echo 'bob ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers; chmod 0440 /etc/sudoers")
        checkout.succeed("cmp /etc/sudoers /etc/static/sudoers")

    with subtest("an edited sshd_config is restored"):
        repaired("rm -f /etc/ssh/sshd_config; echo 'PermitRootLogin yes' > /etc/ssh/sshd_config")
        checkout.succeed("cmp /etc/ssh/sshd_config /etc/static/ssh/sshd_config")

    with subtest("a stopped sshd is started again"):
        repaired("systemctl stop sshd.service")
        checkout.succeed("systemctl is-active sshd.service")
  '';
}
