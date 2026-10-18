# The machine in tests/fleet/safe-directory.nix, built from this flake's own
# modules, with one unprivileged account. Not under hosts/, so it is never a
# flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "checkout";

    users.accounts.bob = {
      fullName = "Bob";
      privileged = false;
    };
  };
}
