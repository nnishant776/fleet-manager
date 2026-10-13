# The hotspot of the `networkd` backend: hostapd on an adapter of its own,
# systemd-networkd giving joining devices addresses and sharing the uplink
# through NAT.
#
# Setting `hotspot.adapter` reserves and names the adapter at once, whatever
# `hotspot.enable` says, so that turning the hotspot on adds the hotspot and
# changes nothing else: the Wi-Fi client keeps its interface and its name.
{ config, lib, ... }:
let
  cfg = config.conf.networking;
  w = cfg.wireless;
  hs = w.hotspot;

  networkd = cfg.enable && cfg.backend == "networkd";
  reserved = networkd && w.enable && hs.adapter != null;
  on = reserved && hs.enable;

  byUsb = hs.adapter == "usb";
  sixG = hs.band == "6g";
  channel = if hs.channel != null then hs.channel
    else { "2g" = 6; "5g" = 36; "6g" = 37; }.${hs.band};
in {
  config = lib.mkMerge [
    (lib.mkIf reserved {
      # Names outside the kernel's wlanN pattern cannot clash with whatever
      # name the kernel hands out at boot, in whichever order adapters appear.
      systemd.network.links."10-hotspot" = {
        matchConfig = { Type = "wlan"; }
          // (if byUsb then { Property = "ID_BUS=usb"; } else { MACAddress = hs.adapter; });
        linkConfig.Name = hs.interface;
      };
      systemd.network.links."11-wifi-uplink" = lib.mkIf (byUsb && w.wifi.interface == null) {
        matchConfig = { Type = "wlan"; Property = "!ID_BUS=usb"; };
        linkConfig.Name = "wlan-uplink";
      };
    })

    (lib.mkIf on {
      services.hostapd = {
        enable = true;
        radios.${hs.interface} = {
          inherit (hs) band;
          inherit channel;
          inherit (w) countryCode;
          # HT and VHT do not exist on 6 GHz, and VHT not on 2.4 GHz.
          wifi4.enable = !sixG;
          wifi5.enable = hs.band == "5g";
          wifi6.enable = true;
          # 6 GHz channels are named by operating class; India and Europe allow
          # indoor (low-power) access points there.
          settings = lib.optionalAttrs sixG { op_class = 131; he_6ghz_reg_pwr_type = 0; }
            // hs.settings;
          networks.${hs.interface} = {
            inherit (hs) ssid;
            authentication = if hs.security == "wpa3" then {
              mode = "wpa3-sae";
              saePasswordsFile = hs.passwordFile;
            } else {
              mode = "wpa3-sae-transition";
              saePasswordsFile = hs.passwordFile;
              wpaPasswordFile = hs.passwordFile;
            };
            # 6 GHz requires protected management frames.
            settings = lib.optionalAttrs sixG { ieee80211w = 2; };
          };
        };
      };

      systemd.network.networks."40-hotspot" = {
        matchConfig.Name = hs.interface;
        address = [ hs.address ];
        networkConfig = {
          DHCPServer = true;
          # Devices on the hotspot reach the uplink through this machine.
          IPMasquerade = "ipv4";
          ConfigureWithoutCarrier = true;
          IPv6AcceptRA = false;
        };
        # DNS servers handed out are the uplink's.
        dhcpServerConfig = {
          EmitDNS = true;
          PoolOffset = 10;
        };
        linkConfig.RequiredForOnline = "no";
      };
      systemd.network.config.networkConfig.IPv4Forwarding = true;

      networking.firewall.interfaces.${hs.interface}.allowedUDPPorts = [ 67 ];
    })

    {
      assertions = lib.optionals (hs.enable || hs.adapter != null) [
        {
          assertion = !hs.enable || hs.adapter != null;
          message = "conf.networking.wireless.hotspot on '${config.conf.host.name}': set `adapter` (\"usb\" or the adapter's MAC address).";
        }
        {
          assertion = !cfg.enable || cfg.backend == "networkd";
          message = "conf.networking.wireless.hotspot on '${config.conf.host.name}' needs conf.networking.backend = \"networkd\".";
        }
        {
          assertion = !hs.enable || w.countryCode != null;
          message = "conf.networking.wireless.hotspot on '${config.conf.host.name}' needs conf.networking.wireless.countryCode; the worldwide rules allow no hotspot on most channels.";
        }
        {
          assertion = byUsb || hs.adapter == null || w.wifi.interface != null || !w.wifi.enable;
          message = "conf.networking.wireless.hotspot.adapter on '${config.conf.host.name}' is a MAC address, so conf.networking.wireless.wifi.interface must name the Wi-Fi client's interface.";
        }
        {
          assertion = !(sixG && hs.security != "wpa3");
          message = "conf.networking.wireless.hotspot on '${config.conf.host.name}': 6 GHz allows WPA3 only; set security = \"wpa3\".";
        }
        {
          assertion = lib.stringLength hs.ssid >= 1 && lib.stringLength hs.ssid <= 32;
          message = "conf.networking.wireless.hotspot.ssid on '${config.conf.host.name}' must be 1 to 32 bytes.";
        }
      ];
    }
  ];
}
