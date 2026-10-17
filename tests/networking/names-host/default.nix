# The machine in tests/networking/names.nix: the `networkd` backend with
# interface names from the MAC, built from this flake's own modules. Not
# under hosts/, so it is never a flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "named";

    networking = {
      backend = "networkd";
      macNames = true;
    };

    users.accounts.admin = {
      fullName = "Admin";
      privileged = true;
    };
  };
}
