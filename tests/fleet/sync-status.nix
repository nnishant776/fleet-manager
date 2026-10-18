# Test that a fleet sync which stops on a problem tells every account, on a
# host built from this flake's modules (tests/fleet/host) whose upstream is a
# local repository. The checks:
# - an unsigned commit stops the sync and is recorded in
#   /var/lib/fleet-sync/status, readable by everyone, with the commit;
# - an unprivileged user sees the notice at login, and `fleet-sync-status`
#   prints it;
# - the same problem on the next run keeps its first time;
# - a different problem (a signed commit that does not build) replaces it.
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
      };
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
  '';
}
