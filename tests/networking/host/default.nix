# The machine in tests/networking: a plain host on the `networkd` backend,
# built from this flake's own modules like any host. Not under hosts/, so it
# is never a flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "netd";

    networking = {
      backend = "networkd";
      wireless = {
        countryCode = "IN";
        wifi = {
          enable = true;
          # The test's radios: wlan0 becomes the router, wlan1 is this machine's.
          interface = "wlan1";
        };
      };
    };

    users.accounts = {
      admin = {
        fullName = "Admin";
        privileged = true;
      };
      bob = {
        fullName = "Bob";
        privileged = false;
      };
    };
  };
}
