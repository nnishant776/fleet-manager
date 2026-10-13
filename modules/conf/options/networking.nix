{ lib, ... }: {
  imports = [
    # The radios share one master switch under `wireless`.
    (lib.mkRenamedOptionModule
      [ "conf" "networking" "wifi" "enable" ]
      [ "conf" "networking" "wireless" "wifi" "enable" ])
    (lib.mkRenamedOptionModule
      [ "conf" "hardware" "bluetooth" "enable" ]
      [ "conf" "networking" "wireless" "bluetooth" "enable" ])
  ];

  options.conf.networking = {
    enable = lib.mkEnableOption "networking, managed as `backend` says";

    backend = lib.mkOption {
      type = lib.types.enum [ "networkmanager" "networkd" ];
      default = "networkmanager";
      description = ''
        What manages the network. `networkmanager` suits machines whose users
        pick networks themselves. `networkd` is for appliances: every wired and
        Wi-Fi interface gets an address by DHCP (systemd-networkd), Wi-Fi
        networks are joined by wpa_supplicant and kept in
        /etc/wpa_supplicant/imperative.conf, and only privileged users can
        change them (`wpa_cli`).
      '';
    };

    # Radios only: wired networking follows `enable` alone, whatever is set here.
    wireless = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Master switch for the radios: the Wi-Fi client and Bluetooth.
          `false` turns them all off whatever their own switches say; `true`
          leaves each to its own switch. Wired networking is not affected.
          Not the NixOS option of the same name, which is wpa_supplicant alone.
        '';
      };
      countryCode = lib.mkOption {
        type = lib.types.nullOr (lib.types.strMatching "[A-Z]{2}");
        default = null;
        example = "IN";
        description = ''
          Two-letter country code whose radio rules apply. Without it the
          strict worldwide rules apply. Used by the `networkd` backend.
        '';
      };

      wifi = {
        enable = lib.mkEnableOption "the Wi-Fi client";
        interface = lib.mkOption {
          type = lib.types.nullOr lib.types.str;
          default = null;
          example = "wlan0";
          description = ''
            `networkd` backend: the one interface the Wi-Fi client uses. `null`
            uses every Wi-Fi interface.
          '';
        };
      };

      bluetooth.enable = lib.mkEnableOption "the Bluetooth stack";
    };

    firewall = {
      enable = lib.mkOption {
        type = lib.types.nullOr lib.types.bool;
        default = null;
        description = ''
          `null` leaves the NixOS firewall at its default, which is on. `true`
          or `false` sets it explicitly; `false` is rejected on a fleet host.
          Ports are opened by the service that owns them, never listed here.
        '';
      };
      config = lib.mkOption {
        type = lib.types.attrs;
        default = { };
        description = "Merged into `networking.firewall`.";
      };
    };
  };
}
