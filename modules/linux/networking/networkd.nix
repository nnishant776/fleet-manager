# The `networkd` backend: systemd-networkd gives every wired interface and
# every Wi-Fi client interface an address by DHCP, and wpa_supplicant joins
# Wi-Fi networks. Meant for appliances; users change networks only through
# `wpa_cli`, which privileged users get (host/privilege.nix).
#
# Wi-Fi networks are kept on the machine, not in this flake. Without named
# interfaces, one wpa_supplicant serves every Wi-Fi interface from
# /etc/wpa_supplicant/imperative.conf: the Raspberry Pi image's first boot
# writes the card's network there, and `wpa_cli save_config` adds to it. Each
# interface named in wifi.interfaces gets a wpa_supplicant of its own, with
# its own networks in /etc/wpa_supplicant/<name>.conf, so different
# interfaces join different networks.
{ config, lib, pkgs, utils, ... }:
let
  cfg = config.conf.networking;
  wifi = cfg.wireless.enable && cfg.wireless.wifi.enable;
  named = cfg.wireless.wifi.interfaces;
  dir = "/etc/wpa_supplicant";

  device = name: "sys-subsystem-net-devices-${utils.escapeSystemdPath name}.device";

  # Before a named interface's wpa_supplicant starts: its file begins as a
  # copy of the shared one, so a machine only on Wi-Fi stays online the first
  # time its interface is named. Both files are kept private.
  prepare = pkgs.writeShellScript "wpa-supplicant-prepare" ''
    set -eu
    file=${dir}/$1.conf
    if [ ! -e "$file" ]; then
      ${pkgs.coreutils}/bin/cp ${dir}/imperative.conf "$file"
    fi
    ${pkgs.coreutils}/bin/chown wpa_supplicant:wpa_supplicant "$file"
    ${pkgs.coreutils}/bin/chmod 0660 "$file" ${dir}/imperative.conf
  '';

  # NixOS's start command for one interface, reading the interface's own
  # file instead of the shared one.
  start = name: let w = config.networking.wireless; in ''
    exec wpa_supplicant -i${name} -s -D${w.driver} \
      -c ${dir}/${name}.conf -I ${dir}/nixos.conf${lib.concatMapStrings (p: " -I " + p) w.extraConfigFiles}
  '';

  namedUnit = name: opts: {
    script = lib.mkForce (start name);
    # Started by its adapter, at boot or whenever it is plugged in, and
    # stopped when it goes; a missing adapter does not hold up the boot.
    wantedBy = lib.mkForce [ (device name) ];
    bindsTo = [ (device name) ];
    serviceConfig = {
      ExecStartPre = lib.mkAfter [ "+${prepare} ${name}" ];
      # All wpa_supplicants keep their control sockets in /run/wpa_supplicant;
      # systemd would delete it when any one of them stops.
      RuntimeDirectoryPreserve = "yes";
    };
  };

  namedNetwork = name: opts: {
    matchConfig.Name = name;
    networkConfig.DHCP = "yes";
    # Which network carries internet traffic when several offer a default
    # route: the lowest metric wins.
    dhcpV4Config = lib.optionalAttrs (opts.routeMetric != null) { RouteMetric = opts.routeMetric; };
    ipv6AcceptRAConfig = lib.optionalAttrs (opts.routeMetric != null) { RouteMetric = opts.routeMetric; };
  };

  # Interface names that do not look like conf.networking.macNames' own.
  odd = lib.filter (n: builtins.match "wl[upsx][0-9a-f]{12}" n == null) (lib.attrNames named);
in {
  config = lib.mkMerge [
    (lib.mkIf (cfg.enable && cfg.backend == "networkd") (lib.mkMerge [
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
          interfaces = lib.attrNames named;
          # Networks in files on the machine, writable and kept across
          # rebuilds, alongside any declared ones.
          allowAuxiliaryImperativeNetworks = true;
          # wpa_cli for the `wpa_supplicant` group, and save_config.
          userControlled = true;
          dbusControlled = false;
          extraConfig = lib.optionalString (cfg.wireless.countryCode != null)
            "country=${cfg.wireless.countryCode}";
        };

        # The network files hold passphrases, but NixOS makes imperative.conf
        # readable by everyone (0664) each time wpa_supplicant starts. Closing
        # the directory keeps them private at all times; the file modes are
        # tightened once started.
        systemd.tmpfiles.rules = [ "d ${dir} 0750 wpa_supplicant wpa_supplicant -" ];
      })

      (lib.mkIf (wifi && named == { }) {
        systemd.network.networks."90-wifi" = {
          matchConfig = { Type = "wlan"; WLANInterfaceType = "station"; };
          networkConfig.DHCP = "yes";
        };
        systemd.services.wpa_supplicant.serviceConfig.ExecStartPre = lib.mkAfter [
            "+${pkgs.coreutils}/bin/chmod 0660 ${dir}/imperative.conf"
          ];
      })

      (lib.mkIf (wifi && named != { }) {
        systemd.services = lib.mapAttrs' (name: opts:
          lib.nameValuePair "wpa_supplicant-${name}" (namedUnit name opts)) named;
        systemd.network.networks = lib.mapAttrs' (name: opts:
          lib.nameValuePair "90-wifi-${name}" (namedNetwork name opts)) named;
      })
    ]))

    {
      assertions = [
        {
          assertion = named == { } || cfg.backend == "networkd";
          message = "conf.networking.wireless.wifi.interfaces on '${config.conf.host.name}' needs conf.networking.backend = \"networkd\".";
        }
        {
          assertion = lib.all (n: lib.stringLength n <= 15) (lib.attrNames named);
          message = "conf.networking.wireless.wifi.interfaces on '${config.conf.host.name}': interface names are at most 15 characters.";
        }
      ];
      warnings = lib.optional (cfg.macNames && odd != [ ])
        ("conf.networking.wireless.wifi.interfaces on '${config.conf.host.name}' names "
          + lib.concatStringsSep ", " odd + ", which conf.networking.macNames would not give a"
          + " Wi-Fi interface (wl, a bus letter, 12 hex digits). Fine for an adapter it leaves"
          + " alone; otherwise a leftover kernel name such as wlan0 that no longer exists.");
    }
  ];
}
