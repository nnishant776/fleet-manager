# The machine in tests/networking/forwarding.nix: a plain host with containers
# on, built from this flake's own modules. Not under hosts/, so it is never a
# flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "fwd";

    containers.enable = true;

    users.accounts.admin = {
      fullName = "Admin";
      privileged = true;
    };
  };
}
