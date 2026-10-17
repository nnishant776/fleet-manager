{ lib, config, ... }: {
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

    macNames = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Name every physical network interface, Wi-Fi and wired, after what it
        is, its bus and its MAC address: `wl` or `en`, then `u` (USB), `p`
        (PCI), `s` (SDIO) or `x` (anything else), then the MAC without
        colons, as in `wlu001a2b3c4d5e`. An adapter keeps its name on every
        boot and in any port. Takes effect at the next boot. Virtual
        interfaces, interfaces a program named itself, and adapters without
        a burned-in MAC keep their names. Independent of every other switch;
        needs the systemd initrd. Anything that names an interface must use
        the new names.
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
        interfaces = lib.mkOption {
          type = lib.types.attrsOf (lib.types.submodule {
            options.routeMetric = lib.mkOption {
              type = lib.types.nullOr lib.types.ints.unsigned;
              default = null;
              example = 2048;
              description = ''
                Which network carries internet traffic when several offer a
                default route: the lowest metric wins. `null` leaves
                networkd's default, 1024, the same as a wired network gets.
              '';
            };
          });
          default = { };
          example = lib.literalExpression ''
            {
              wlsd83add1a2b3c = { };
              wlu001a2b3c4d5e.routeMetric = 2048;
            }
          '';
          description = ''
            `networkd` backend: the interfaces the Wi-Fi client uses, by name.
            Each gets a wpa_supplicant of its own, with its own networks in
            /etc/wpa_supplicant/<name>.conf, so different interfaces join
            different networks; the file starts as a copy of
            /etc/wpa_supplicant/imperative.conf. Each starts when its adapter
            appears. `{ }` uses every Wi-Fi interface, all sharing
            imperative.conf; with `hotspot.adapter = "usb"`, every one that is
            not on USB, named `wlan-uplink`.
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
            `wifi.interfaces` must name the client's interfaces). Setting it reserves the adapter for
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
        description = ''
          Merged into `networking.firewall`, for its options such as logging.
          The firewall is nftables; rules of the host's own go in
          `inputRules`, `forwardRules` or `tables`, and `extraCommands` is
          rejected.
        '';
      };

      # The three options below are this host's own additions and exceptions.
      # Rules that follow from what a host does (its role, its services, its
      # containers, VMs or hotspots) are set by the module that sets that up;
      # modules never use these (asserted in linux/networking/firewall.nix).
      inputRules = lib.mkOption {
        type = lib.types.lines;
        default = "";
        example = "ip saddr 192.168.1.0/24 tcp dport 22 accept";
        description = ''
          nftables rules of this host's own for new connections to it. They
          come after the ports the firewall opens, so they add access (SSH
          from one network only, say); they cannot take away what an open
          port allows. For that, use `tables`.
        '';
      };
      forwardRules = lib.mkOption {
        type = lib.types.lines;
        default = "";
        description = ''
          nftables rules of this host's own for new connections passing
          through it. Setting any turns on "forwarding denied unless
          allowed" for this host.
        '';
      };
      tables = lib.mkOption {
        type = lib.types.attrsOf (lib.types.submodule {
          options = {
            family = lib.mkOption {
              type = lib.types.enum [ "ip" "ip6" "inet" "arp" "bridge" "netdev" ];
              default = "inet";
              description = "The table's family.";
            };
            content = lib.mkOption {
              type = lib.types.lines;
              description = "The table's chains, sets and maps.";
            };
          };
        });
        default = { };
        description = ''
          Whole nftables tables of this host's own, for what `inputRules`
          and `forwardRules` cannot say: chains at any hook and priority,
          address translation, sets, maps. In nftables a `drop` anywhere is
          final, while an `accept` only ends its own chain and the other
          tables still see the packet; so blocking something the firewall
          allows takes a chain at an earlier priority (`filter - 1`). Names
          other tables already use are rejected.
        '';
      };
    };
  };
}
