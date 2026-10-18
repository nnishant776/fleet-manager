# Test that every account can use the root-owned configuration checkout
# (conf.fleet.localPath) on a host built from this flake's modules
# (tests/fleet/host). The checks:
# - an unprivileged user with no git configuration of their own evaluates the
#   flake at /etc/nixos, and git works there for them too;
# - a root-owned repository anywhere else is still refused, so only the
#   checkout is trusted.
# Run it with `nix build .#checks.x86_64-linux.fleet-safe-directory`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "checkout"; hostDir = ./host; };

  # No inputs, so evaluating it needs no network.
  flake = pkgs.writeText "flake.nix" ''
    { outputs = _: { x = "ok"; }; }
  '';

  # Creates a git repository holding that flake at the given path, owned by
  # whoever runs it, which in the test is root.
  mkRepo = pkgs.writeShellScript "mk-repo" ''
    set -eu
    mkdir -p "$1"
    cp ${flake} "$1/flake.nix"
    cd "$1"
    ${lib.getExe pkgs.git} init -q
    ${lib.getExe pkgs.git} add flake.nix
    ${lib.getExe pkgs.git} -c user.name=root -c user.email=root@localhost commit -q -m init
  '';
in
pkgs.testers.runNixOSTest {
  name = "fleet-safe-directory";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.checkout = {
    imports = hostNode.modules ++ [{
      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;
    }];
  };

  testScript = ''
    checkout.wait_for_unit("multi-user.target")
    checkout.succeed("${mkRepo} /etc/nixos", "${mkRepo} /srv/other")

    with subtest("an unprivileged user uses the root-owned checkout"):
        # Nothing of bob's own trusts it.
        checkout.fail("grep -qs safe /home/bob/.gitconfig /home/bob/.config/git/config")
        out = checkout.succeed("runuser -u bob -- nix eval --raw /etc/nixos#x")
        assert out == "ok", out
        checkout.succeed("runuser -u bob -- git -C /etc/nixos status")

    with subtest("a root-owned repository elsewhere is still refused"):
        out = checkout.fail("runuser -u bob -- nix eval --raw /srv/other#x 2>&1")
        assert "not owned by current user" in out, out
        checkout.fail("runuser -u bob -- git -C /srv/other status")
  '';
}
