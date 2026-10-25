# Test of `fleet host enroll` on a running host that has a checkout in
# /etc/nixos but no secrets identity yet: the case where the first-boot clone
# never runs, so the identity has to be made by hand. A `forge` node serves a
# fresh, signed secrets repository over SSH and also plays the maintainer's
# machine; `newhost` is built from tests/fleet/enroll-host. The checks:
# - fleet logs in as admin through a one-key agent, creates the identity,
#   copies the enrollment file and checks it, enrolls the host with a signed
#   commit, pushes, and registers the deploy key;
# - the host can then fetch, verify and decrypt its secrets with that key;
# - a second run finds everything done and changes nothing;
# - the script enroll runs after a host's first switch makes /etc/nixos the
#   configuration's checkout, whether it held a checkout of something else or
#   plain files, with a stub in place of the first-boot clone, fails clearly
#   when the clone fails, and deletes nothing on a host without the unit.
# Run it with `nix build .#checks.x86_64-linux.fleet-enroll`.
{ pkgs, inputs, self }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "newhost"; hostDir = ./enroll-host; };
  remakeScript = ../../tools/fleet/flows/remake-etc-nixos.sh;

  fixtures = pkgs.runCommand "fleet-enroll-fixtures" {
    nativeBuildInputs = with pkgs; [ age openssh git ];
  } ''
    export HOME="$TMPDIR/home"
    mkdir -p "$HOME" "$out"
    age-keygen -o "$out/recovery.key" 2>/dev/null
    for key in signing admin support; do
      ssh-keygen -q -t ed25519 -N "" -C "$key@test" -f "$out/$key"
    done
    echo "maintainer@test namespaces=\"git\" $(cut -d' ' -f1,2 "$out/signing.pub")" > "$out/allowed_signers"

    signed_git() {
      git -c gpg.format=ssh -c "user.signingkey=$out/signing" -c commit.gpgsign=true \
        -c user.name=maintainer -c user.email=maintainer@test "$@"
    }

    # What fleet setup caches for the maintainer's tools.
    age-keygen -y "$out/recovery.key" > "$out/recovery.pub"

    # A secrets repository as fleet-secrets starts: main holds a README, and
    # there is no host branch yet.
    git init -q --bare -b main "$out/secrets.git"
    git init -q -b main "$TMPDIR/secrets"
    (
      cd "$TMPDIR/secrets"
      echo "# Secrets" > README.md
      printf '*.enroll\n' > .gitignore
      git add -A
      signed_git commit -q -m "fleet-secrets, empty"
      git push -q "$out/secrets.git" main
    )
  '';

  secretsUrl = "ssh://git@forge/var/lib/forge/secrets.git";
in
pkgs.testers.runNixOSTest {
  name = "fleet-enroll";

  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.forge = { pkgs, ... }: {
    services.openssh.enable = true;
    users.users.git.isNormalUser = true;
    environment.systemPackages = [ pkgs.git self.packages.${pkgs.stdenv.hostPlatform.system}.fleet ];

    systemd.services.forge-repos = {
      description = "Serve the fixture secrets repository, with deploy keys registered in a file";
      wantedBy = [ "multi-user.target" ];
      before = [ "sshd.service" ];
      requiredBy = [ "sshd.service" ];
      serviceConfig.Type = "oneshot";
      script = ''
        mkdir -p /var/lib/forge /home/git/.ssh
        cp -r --no-preserve=mode ${fixtures}/secrets.git /var/lib/forge/
        touch /home/git/.ssh/authorized_keys
        chmod 700 /home/git/.ssh
        chmod 600 /home/git/.ssh/authorized_keys
        chown -R git:users /var/lib/forge /home/git/.ssh
      '';
    };
  };

  nodes.newhost = { config, ... }: {
    imports = hostNode.modules ++ [{
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      conf.users.accounts.admin = {
        sshKeys = [ (lib.removeSuffix "\n" (builtins.readFile "${fixtures}/admin.pub")) ];
        sudoSshKeys = [ (lib.removeSuffix "\n" (builtins.readFile "${fixtures}/admin.pub")) ];
      };
      conf.fleet = {
        repo.url = "ssh://git@forge/var/lib/forge/config.git";
        signing.allowedSignersFile = "${fixtures}/allowed_signers";
        support.sshKeys = [ (lib.removeSuffix "\n" (builtins.readFile "${fixtures}/support.pub")) ];
      };
      conf.secrets.repo.url = secretsUrl;

      # The test makes the checkout itself and runs the secrets sync by hand.
      systemd.services.fleet-bootstrap.enable = false;
      systemd.timers.config-auto-update.enable = false;

      # Root reaches the forge with the host's deploy key, once registered.
      programs.ssh.extraConfig = ''
        Host forge
          IdentityFile /var/lib/fleet/secrets_deploy_key
          IdentitiesOnly yes
          StrictHostKeyChecking accept-new
      '';
    }];
  };

  testScript = ''
    start_all()
    forge.wait_for_unit("sshd.service")
    newhost.wait_for_unit("sshd.service")

    # A running host with a checkout, as neerajpinew had: the first-boot
    # clone never runs, so no identity exists.
    newhost.succeed(
        "mkdir -p /etc/nixos && git -C /etc/nixos init -q",
        "git -C /etc/nixos -c user.name=x -c user.email=x@test commit -q --allow-empty -m local",
    )
    newhost.fail("test -e /var/lib/fleet/age.key")

    # The forge as the maintainer's machine: keys, the recovery recipient as
    # fleet setup caches it, a signing clone, the host's key known.
    forge.succeed(
        "install -o git -m 600 ${fixtures}/recovery.key ${fixtures}/signing ${fixtures}/admin /home/git/",
        "ssh-keyscan -t ed25519 newhost >> /home/git/.ssh/known_hosts 2>/dev/null",
        "chown git /home/git/.ssh/known_hosts",
        "su git -c 'mkdir -p ~/.local/state/fleet && cp ${fixtures}/recovery.pub ~/.local/state/fleet/recovery.pub'",
        "su git -c 'git clone -q /var/lib/forge/secrets.git /home/git/w"
        " && git -C /home/git/w config user.name maintainer"
        " && git -C /home/git/w config user.email maintainer@test"
        " && git -C /home/git/w config gpg.format ssh"
        " && git -C /home/git/w config user.signingKey /home/git/signing"
        " && git -C /home/git/w config commit.gpgSign true"
        " && git -C /home/git/w config gpg.ssh.allowedSignersFile ${fixtures}/allowed_signers'",
    )

    def enroll():
        # Bounded and printed, so that a step that hangs shows where.
        status, out = forge.execute(
            "cd /home/git && timeout 180 su git -c 'SOPS_AGE_KEY_FILE=/home/git/recovery.key"
            " fleet host enroll newhost --flake /var/empty --key /home/git/admin"
            " --repo /home/git/w --recovery \"\" --sync=false --yes"
            " --register-with \"cat >> /home/git/.ssh/authorized_keys\"' 2>&1"
        )
        print(out)
        assert status == 0, f"fleet host enroll exited {status}"
        return out

    with subtest("fleet enrolls a running host without an identity"):
        out = enroll()
        for want in ["has no identity yet", "created under /var/lib/fleet", "checksum",
                     "committed: host-enroll: newhost", "stopped the one-key agent"]:
            assert want in out, f"output lacks {want!r}:\n{out}"
        newhost.succeed("test -f /var/lib/fleet/age.key -a -f /var/lib/fleet/secrets_deploy_key")
        # On the host's own branch, which main knows nothing of.
        forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git log --format=%s host/newhost' | grep -x 'host-enroll: newhost'")
        forge.fail("su git -c 'git -C /var/lib/forge/secrets.git log --format=%s main' | grep -x 'host-enroll: newhost'")
        forge.succeed(
            "su git -c 'git -C /var/lib/forge/secrets.git -c gpg.ssh.allowedSignersFile=${fixtures}/allowed_signers"
            " verify-commit host/newhost'"
        )
        key = newhost.succeed("cut -d' ' -f1,2 /var/lib/fleet/secrets_deploy_key.pub").strip()
        forge.succeed(f"grep -qF '{key}' /home/git/.ssh/authorized_keys")

    with subtest("the host then fetches, verifies and decrypts its secrets"):
        newhost.succeed(
            "fleet-secrets-sync sync --state /var/lib/fleet --host newhost"
            " --repo ${secretsUrl} --ref host/newhost --signers ${fixtures}/allowed_signers"
        )
        newhost.succeed("test -f /var/lib/fleet/secrets/hosts/newhost.yaml")

    with subtest("a second run finds everything done"):
        before = forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/newhost'")
        out = enroll()
        for want in ["skipped: it has one", "skipped: it is enrolled already, with this identity",
                     "skipped: nothing to push"]:
            assert want in out, f"output lacks {want!r}:\n{out}"
        after = forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/newhost'")
        assert before == after, "a second run changed the secrets repository"

    with subtest("/etc/nixos is replaced by the configuration's checkout, whatever it held"):
        # What enroll runs on a host after its first switch, on scratch
        # directories in place of /etc/nixos and /var/lib/fleet-sync. This test
        # keeps the host's own fleet-bootstrap off and has no configuration
        # repository on the forge, and a system built here records no revision
        # to accept, so the unit cannot make a real clone: a stub systemctl
        # does what the unit does when it is started and finds no checkout
        # (its condition), which is the one thing the script relies on.
        newhost.succeed(
            "git init -q --bare -b main /srv/config.git",
            "git clone -q /srv/config.git /tmp/c",
            "git -C /tmp/c -c user.name=x -c user.email=x@test commit -q --allow-empty -m one",
            "git -C /tmp/c push -q origin HEAD:main",
            "mkdir -p /tmp/stub",
            "printf '%s\\n' '#!/bin/sh'"
            " 'if [ \"$1\" = cat ]; then [ -z \"$NO_UNIT\" ]; exit; fi'"
            " '[ -d \"$DIR/.git\" ] && exit 0'"
            " 'rm -rf \"$DIR\"'"
            " 'git clone -q --branch main \"$URL\" \"$DIR\"'"
            " 'mkdir -p \"$STATE\"'"
            " 'git -C \"$DIR\" rev-parse HEAD > \"$STATE/accepted\"'"
            " 'echo \"$URL\" > \"$STATE/accepted.url\"' > /tmp/stub/systemctl",
            "chmod +x /tmp/stub/systemctl",
        )
        one = newhost.succeed("git -C /srv/config.git rev-parse main").strip()
        url = "file:///srv/config.git"
        env = f"PATH=/tmp/stub:$PATH URL={url} DIR=/tmp/etc-nixos STATE=/tmp/fleet-sync"
        git = "git -C /tmp/etc-nixos"

        def check_replaced():
            assert newhost.succeed(f"{git} rev-parse HEAD").strip() == one
            assert newhost.succeed(f"{git} remote").strip() == "origin"
            assert newhost.succeed(f"{git} remote get-url origin").strip() == url
            assert newhost.succeed(f"{git} branch --format=\"%(refname:short)\"").strip() == "main"
            newhost.fail("test -e /tmp/etc-nixos/mine.nix")
            assert newhost.succeed("cat /tmp/fleet-sync/accepted").strip() == one
            assert newhost.succeed("cat /tmp/fleet-sync/accepted.url").strip() == url

        # A checkout of something else, with a branch of its own, files that
        # were never committed, and an older record of what the sync accepted.
        newhost.succeed(
            "git init -q -b mine /tmp/etc-nixos",
            "git -C /tmp/etc-nixos -c user.name=x -c user.email=x@test commit -q --allow-empty -m mine",
            "git -C /tmp/etc-nixos remote add origin https://example.org/mine.git",
            "touch /tmp/etc-nixos/mine.nix",
            "mkdir -p /tmp/fleet-sync && echo old > /tmp/fleet-sync/accepted && echo https://example.org/mine.git > /tmp/fleet-sync/accepted.url",
        )
        out = newhost.succeed(f"{env} sh ${remakeScript} {url} true /tmp/etc-nixos /tmp/fleet-sync")
        assert out.strip() == one, out
        check_replaced()

        # Plain files: the unit finds no checkout and replaces them.
        newhost.succeed("rm -rf /tmp/etc-nixos /tmp/fleet-sync", "mkdir /tmp/etc-nixos", "touch /tmp/etc-nixos/mine.nix")
        out = newhost.succeed(f"{env} sh ${remakeScript} {url} false /tmp/etc-nixos /tmp/fleet-sync")
        assert out.strip() == one, out
        check_replaced()

        # A clone that fails leaves the script failing, and says the unit goes on trying.
        newhost.succeed("rm -rf /tmp/etc-nixos /tmp/fleet-sync", "mkdir /tmp/etc-nixos", "touch /tmp/etc-nixos/mine.nix")
        status, out = newhost.execute(
            "PATH=/tmp/stub:$PATH URL=file:///srv/missing.git DIR=/tmp/etc-nixos STATE=/tmp/fleet-sync"
            " sh ${remakeScript} file:///srv/missing.git false /tmp/etc-nixos /tmp/fleet-sync 2>&1"
        )
        assert status != 0 and "keeps trying every 60 seconds" in out, out

        # A host without the unit could not make the checkout again, so
        # nothing is deleted.
        newhost.succeed("rm -rf /tmp/etc-nixos", "mkdir /tmp/etc-nixos", "touch /tmp/etc-nixos/mine.nix")
        status, out = newhost.execute(f"NO_UNIT=1 {env} sh ${remakeScript} {url} true /tmp/etc-nixos /tmp/fleet-sync 2>&1")
        assert status != 0 and "left as it is" in out, out
        newhost.succeed("test -e /tmp/etc-nixos/mine.nix")
  '';
}
