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
          # The test's radios: wlan0 and wlan1 become the routers, wlan2 and
          # wlan3 are this machine's, on different networks. wlan2 carries
          # internet traffic: its metric is below wlan3's and the VM's wired
          # card's, both networkd's default, 1024.
          interfaces = {
            wlan2.routeMetric = 512;
            wlan3 = { };
          };
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
