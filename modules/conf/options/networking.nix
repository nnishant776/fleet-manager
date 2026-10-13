{ lib, config, ... }: {
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
          Master switch for the radios: the Wi-Fi client, the hotspot and
          Bluetooth. `false` turns them all off whatever their own switches
          say; `true` leaves each to its own switch. Wired networking is not
          affected.
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
            uses every Wi-Fi interface; with `hotspot.adapter = "usb"`, every
            one that is not on USB, named `wlan-uplink`.
          '';
        };
      };

      hotspot = {
        enable = lib.mkEnableOption ''
          a Wi-Fi hotspot that shares this machine's connection, on its own
          adapter (hostapd). Needs the `networkd` backend, `countryCode`,
          `adapter` and the password in `passwordFile`
        '';

        adapter = lib.mkOption {
          type = lib.types.nullOr (lib.types.either (lib.types.enum [ "usb" ])
            (lib.types.strMatching "([0-9a-f]{2}:){5}[0-9a-f]{2}"));
          default = null;
          example = "usb";
          description = ''
            Which Wi-Fi adapter runs the hotspot: `"usb"` for the machine's one
            USB Wi-Fi adapter, or the adapter's MAC address in lower case (then
            `wifi.interface` must be set). Setting it reserves the adapter for
            the hotspot and names it `interface`, even while the hotspot is
            off, so turning the hotspot on changes nothing else.
          '';
        };

        interface = lib.mkOption {
          type = lib.types.str;
          default = "wlan-ap";
          description = "Name given to the hotspot's adapter.";
        };

        ssid = lib.mkOption {
          type = lib.types.str;
          default = config.conf.host.name;
          defaultText = lib.literalExpression "config.conf.host.name";
          description = "Network name the hotspot shows, 1 to 32 bytes.";
        };

        band = lib.mkOption {
          type = lib.types.enum [ "2g" "5g" "6g" ];
          default = "5g";
          description = ''
            Frequency band. `6g` (Wi-Fi 6E) needs an adapter and a country that
            allow a hotspot there; it always uses WPA3, as the band requires.
          '';
        };

        channel = lib.mkOption {
          type = lib.types.nullOr lib.types.ints.positive;
          default = null;
          description = "Channel within `band`; `null` picks 6 on `2g`, 36 on `5g` and 37 on `6g`.";
        };

        security = lib.mkOption {
          type = lib.types.enum [ "wpa3" "wpa2-wpa3" ];
          default = "wpa3";
          description = ''
            `wpa3` lets in WPA3 devices only. `wpa2-wpa3` also lets in older
            WPA2 devices, with WPA2's weaker protection for them; not on `6g`.
          '';
        };

        address = lib.mkOption {
          type = lib.types.strMatching "[0-9]+\\.[0-9]+\\.[0-9]+\\.[0-9]+/[0-9]+";
          default = "10.42.0.1/24";
          description = ''
            The hotspot's own address and subnet. Devices that join get an
            address in it and reach the internet through this machine.
          '';
        };

        passwordFile = lib.mkOption {
          type = lib.types.str;
          default = "/var/lib/hotspot/password";
          description = ''
            File on the machine holding only the hotspot password, 8 to 63
            characters, without `|`. Read when the hotspot starts, never copied
            into the Nix store. Create it as root, mode 0600.
          '';
        };

        settings = lib.mkOption {
          type = lib.types.attrsOf lib.types.anything;
          default = { };
          example = { he_oper_chwidth = 1; };
          description = "Extra hostapd settings for the hotspot's radio.";
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
