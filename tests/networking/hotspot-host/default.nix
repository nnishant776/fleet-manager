# The machine in tests/networking/hotspot.nix: the `networkd` backend with a
# Wi-Fi uplink and a hotspot, built from this flake's own modules. Not under
# hosts/, so it is never a flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "hotspot5";

    networking = {
      backend = "networkd";
      wireless = {
        countryCode = "IN";
        # The test's radios: wlan0 the router, wlan1 this machine's uplink,
        # wlan2 its hotspot, wlan3 a phone.
        wifi = {
          enable = true;
          interfaces.wlan1 = { };
        };
        hotspot = {
          enable = true;
          # mac80211_hwsim gives radio N the address 02:00:00:00:0N:00.
          adapter = "02:00:00:00:02:00";
          ssid = "pi hotspot";
        };
      };
    };

    users.accounts.admin = {
      fullName = "Admin";
      privileged = true;
    };
  };
}
