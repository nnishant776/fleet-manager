# The machine in tests/networking/hotspot.nix: the `networkd` backend with a
# Wi-Fi uplink and several hotspots, built from this flake's own modules. Not
# under hosts/, so it is never a flake output.
#
# Names come from the MAC (macNames): mac80211_hwsim gives radio N the
# address 02:00:00:00:0N:00, so radio N is wlx02000000<N>00.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "hotspots";

    networking = {
      backend = "networkd";
      macNames = true;
      wireless = {
        countryCode = "IN";
        # Radio 0 is the router, 1 this machine's uplink, 2 and 3 hotspots
        # A and B, 4 to 7 phones, 8 and 9 hotspots E and F.
        wifi = {
          enable = true;
          interfaces.wlx020000000100 = { };
        };
        hotspots = {
          # A: 5 GHz, WPA3.
          wlx020000000200 = {
            enable = true;
            ssid = "pi a";
            band = "5g";
          };
          # B: 6 GHz, one device at a time, as a custom hostapd setting.
          wlx020000000300 = {
            enable = true;
            ssid = "pi b";
            band = "6g";
            address = "10.43.0.1/24";
            settings.max_num_sta = 1;
          };
          # C: turned on, but its adapter is not there.
          wlx020000009900 = {
            enable = true;
            ssid = "pi c";
            address = "10.44.0.1/24";
          };
          # E: a custom line hostapd does not know, so it fails alone.
          wlx020000000800 = {
            enable = true;
            ssid = "pi e";
            band = "2g";
            address = "10.45.0.1/24";
            extraConfig = "not_a_hostapd_key=1";
          };
          # F: 5 GHz, a pinned width rather than the widest.
          wlx020000000900 = {
            enable = true;
            ssid = "pi f";
            channel = 149;
            width = 40;
            address = "10.46.0.1/24";
          };
        };
      };
    };

    users.accounts.admin = {
      fullName = "Admin";
      privileged = true;
    };
  };
}
