# The `networkd` backend: systemd-networkd gives every wired interface and
# every Wi-Fi client interface an address by DHCP, and wpa_supplicant joins
# Wi-Fi networks. Meant for appliances; users change networks only through
# `wpa_cli`, which privileged users get (host/privilege.nix).
#
# Wi-Fi networks live in /etc/wpa_supplicant/imperative.conf, a file the
# machine owns rather than this flake: the Raspberry Pi image's first boot
# writes the card's network there, and `wpa_cli save_config` adds to it.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.networking;
  wifi = cfg.wireless.enable && cfg.wireless.wifi.enable;
  iface = cfg.wireless.wifi.interface;
  # The units NixOS generates: one per listed interface, or one for all.
  wpaUnit = if iface == null then "wpa_supplicant" else "wpa_supplicant-${iface}";
in {
  config = lib.mkIf (cfg.enable && cfg.backend == "networkd") (lib.mkMerge [
    {
      networking.useNetworkd = true;
      # The DHCP networks are the ones below, not NixOS' catch-all ones.
      networking.useDHCP = false;
      systemd.network.enable = true;
      # Online as soon as any interface is; a machine without its cable must
      # not wait at boot for it.
      systemd.network.wait-online.anyInterface = true;

      systemd.network.networks."90-wired" = {
        matchConfig.Type = "ether";
        networkConfig.DHCP = "yes";
      };

      assertions = [{
        assertion = !config.networking.networkmanager.enable;
        message = "conf.networking.backend = \"networkd\" on '${config.conf.host.name}', but something turns NetworkManager on.";
      }];
    }

    (lib.mkIf wifi {
      networking.wireless = {
        enable = true;
        interfaces = lib.optional (iface != null) iface;
        # Networks in /etc/wpa_supplicant/imperative.conf, writable and kept
        # across rebuilds, alongside any declared ones.
        allowAuxiliaryImperativeNetworks = true;
        # wpa_cli for the `wpa_supplicant` group, and save_config.
        userControlled = true;
        dbusControlled = false;
        extraConfig = lib.optionalString (cfg.wireless.countryCode != null)
          "country=${cfg.wireless.countryCode}";
      };

      systemd.network.networks."90-wifi" = {
        matchConfig = if iface != null
          then { Name = iface; }
          else { Type = "wlan"; WLANInterfaceType = "station"; };
        networkConfig.DHCP = "yes";
      };

      # NixOS leaves imperative.conf readable by everyone (0664), and it holds
      # passphrases; only wpa_supplicant and its group may read it.
      systemd.services.${wpaUnit}.serviceConfig.ExecStartPre = lib.mkAfter [
        "+${pkgs.coreutils}/bin/chmod 0660 /etc/wpa_supplicant/imperative.conf"
      ];
    })
  ]);
}
