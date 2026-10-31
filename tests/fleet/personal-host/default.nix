# The machine in tests/fleet/personal.nix, built from this flake's own modules,
# with one ordinary fleet account, bob, who may log in over SSH. Not under
# hosts/, so it is never a flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "checkout";
    sharing.ssh.enable = true;

    users.accounts.bob = {
      fullName = "Bob";
      privileged = false;
      groups = [ "ssh-users" ];
    };
  };
}
