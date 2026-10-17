{ lib, config, ... }:
let
  # A hostapd value, in hostapd.conf's own types.
  hostapdAtom = lib.types.oneOf [ lib.types.bool lib.types.int lib.types.str ];
in {
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
            imperative.conf; with any hotspot declared, the client's
            interfaces must be named, so the client never takes a hotspot's
            adapter.
          '';
        };
      };

      hotspots = lib.mkOption {
        type = lib.types.attrsOf (lib.types.submodule ({ name, ... }: {
          options = {
            enable = lib.mkEnableOption ''
              this hotspot. Off, its adapter is still kept from the Wi-Fi
              client, so turning it on changes nothing else'';

            ssid = lib.mkOption {
              type = lib.types.str;
              default = config.conf.host.name;
              defaultText = lib.literalExpression "config.conf.host.name";
              description = "Network name the hotspot shows, 1 to 32 bytes; each hotspot its own.";
            };

            band = lib.mkOption {
              type = lib.types.enum [ "2g" "5g" "6g" ];
              default = "5g";
              description = ''
                Frequency band. `6g` (Wi-Fi 6E) needs an adapter and a country
                that allow a hotspot there; it always uses WPA3, as the band
                requires.
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
                Hotspots' subnets must not overlap.
              '';
            };

            passwordFile = lib.mkOption {
              type = lib.types.str;
              default = "/var/lib/hotspot/${name}";
              defaultText = lib.literalExpression ''"/var/lib/hotspot/<name>"'';
              description = ''
                File on the machine holding only this hotspot's password, 8 to
                63 characters, without `|`. Read when the hotspot starts, never
                copied into the Nix store. Create it as root, mode 0600.
              '';
            };

            settings = lib.mkOption {
              type = lib.types.attrsOf (lib.types.either hostapdAtom (lib.types.listOf hostapdAtom));
              default = { };
              example = { max_num_sta = 16; ap_max_inactivity = 600; };
              description = ''
                hostapd settings of this hotspot's own, added or overriding the
                flake's: they come last, so they win. `true`/`false` become
                1/0; a list writes the key once per item. Not `interface` or
                `ctrl_interface`, which the service depends on. Not for
                secrets: this ends up in the Nix store.
              '';
            };

            extraConfig = lib.mkOption {
              type = lib.types.lines;
              default = "";
              description = ''
                Lines added to this hotspot's hostapd configuration word for
                word, after `settings`, for what `settings` cannot say. The
                same limits apply. A line hostapd rejects stops this hotspot
                only.
              '';
            };
          };
        }));
        default = { };
        example = lib.literalExpression ''
          {
            wlu00c0ca9c8d7e = { enable = true; ssid = "pi-guest"; };
            wlu00c0ca0a1b2c = { ssid = "pi-iot"; band = "2g"; address = "10.43.0.1/24"; };
          }
        '';
        description = ''
          `networkd` backend: Wi-Fi hotspots that share this machine's
          connection, one per adapter, keyed by the adapter's interface name.
          Each runs in a service of its own (`hostapd-<name>`), started when
          its adapter appears, so one adapter missing stops only its own
          hotspot. Needs `countryCode`, each hotspot's password in its
          `passwordFile`, and the Wi-Fi client's interfaces named in
          `wifi.interfaces`.
        '';
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
