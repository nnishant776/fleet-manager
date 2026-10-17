# Hotspots of the `networkd` backend. Each adapter named in
# conf.networking.wireless.hotspots runs a network of its own, in a hostapd
# service of its own (hostapd-<name>), so one adapter missing or unplugged
# stops only its own hotspot. Each uses the widest channel its adapter and
# the country allow (`width = "max"`, hotspot-start.nix). systemd-networkd
# gives joining devices addresses and shares the uplink through NAT.
#
# This flake writes hostapd's configuration itself rather than through
# NixOS's hostapd module, which runs every radio in one service bound to all
# their adapters. What that module did is carried over: the password read at
# start and never stored, the WPA3 and WPA2/WPA3 settings, 802.11d/h, the
# control socket, the sandboxing and the regulatory database.
{ config, lib, pkgs, utils, ... }:
let
  cfg = config.conf.networking;
  w = cfg.wireless;
  hotspots = w.hotspots;
  active = lib.filterAttrs (_: h: h.enable) hotspots;
  networkd = cfg.enable && cfg.backend == "networkd";
  on = networkd && w.enable && active != { };

  # Hotspots whose devices may reach only DHCP on this machine.
  guarded = lib.attrNames (lib.filterAttrs (_: h: !h.hostAccess) active);
  nftSet = names: "{ ${lib.concatMapStringsSep ", " (n: ''"${n}"'') names} }";

  device = name: "sys-subsystem-net-devices-${utils.escapeSystemdPath name}.device";

  # hostapd's key=value format, as NixOS's module writes it: true and false
  # as 1 and 0, a list as the key once per item.
  toHostapd = lib.generators.toKeyValue {
    listsAsDuplicateKeys = true;
    mkKeyValue = lib.generators.mkKeyValueDefault {
      mkValueString = v:
        if lib.isInt v then toString v
        else if lib.isString v then v
        else if v == true then "1"
        else if v == false then "0"
        else throw "hostapd setting of type ${builtins.typeOf v}";
    } "=";
  };

  channelOf = h: if h.channel != null then h.channel
    else { "2g" = 6; "5g" = 36; "6g" = 37; }.${h.band};

  # What the flake sets for every hotspot.
  settingsOf = name: h:
    let sixG = h.band == "6g"; in {
      interface = name;
      driver = "nl80211";
      ctrl_interface = "/run/hostapd-${name}";
      ctrl_interface_group = "wheel";
      logger_syslog = -1;
      logger_syslog_level = 2;
      logger_stdout = -1;
      logger_stdout_level = 2;

      ssid = h.ssid;
      utf8_ssid = true;
      hw_mode = if h.band == "2g" then "g" else "a";
      channel = channelOf h;
      country_code = w.countryCode;
      # Announce the country, and handle radar where the rules require it.
      ieee80211d = true;
      ieee80211h = true;
      # HT and VHT do not exist on 6 GHz, and VHT not on 2.4 GHz.
      ieee80211n = !sixG;
      ieee80211ac = h.band == "5g";
      ieee80211ax = true;
      wmm_enabled = true;
      auth_algs = 1;
      # Devices on the same hotspot see each other: a requirement.
      ap_isolate = false;

      wpa = 2;
      rsn_pairwise = "CCMP";
      # Protected management frames: required on 6 GHz and with WPA3 alone.
      ieee80211w = if sixG || h.security == "wpa3" then 2 else 1;
      sae_require_mfp = true;
      # Both ways of deriving the SAE password element.
      sae_pwe = 2;
    }
    // (if h.security == "wpa3" then {
      wpa_key_mgmt = "SAE";
      # Tells WPA3 devices to stop offering anything older.
      transition_disable = "0x01";
    } else {
      # WPA2 devices with and without protected management frames.
      wpa_key_mgmt = "WPA-PSK WPA-PSK-SHA256 SAE";
    })
    # India and Europe allow indoor (low-power) access points on 6 GHz. Its
    # operating class follows the width (hotspot-start.nix).
    // lib.optionalAttrs sixG { he_6ghz_reg_pwr_type = 0; };

  # Built with the system: the flake's settings first, the host's own last.
  baseFile = name: h: pkgs.writeText "hostapd-${name}.conf" (toHostapd (settingsOf name h));
  customFile = name: h: pkgs.writeText "hostapd-${name}-custom.conf"
    (toHostapd h.settings + lib.optionalString (h.extraConfig != "") "\n${h.extraConfig}\n");

  # Before hostapd starts: the flake's settings and the password, read from
  # its file, in the runtime directory, root-only. The start script adds the
  # width and then the host's own settings; hostapd keeps the last value it
  # reads for a key, so those win.
  prepare = name: h: pkgs.writeShellScript "hostapd-${name}-prepare" ''
    set -eu
    password=$(${pkgs.coreutils}/bin/cat ${lib.escapeShellArg h.passwordFile})
    case ''${#password} in
      [8-9]|[1-5][0-9]|6[0-3]) ;;
      *) echo "${h.passwordFile}: the password must be 8 to 63 characters" >&2; exit 1 ;;
    esac
    case $password in
      *'|'*) echo "${h.passwordFile}: the password must not contain |" >&2; exit 1 ;;
    esac
    {
      ${pkgs.coreutils}/bin/cat ${baseFile name h}
      printf 'sae_password=%s\n' "$password"
      ${lib.optionalString (h.security != "wpa3") ''printf 'wpa_passphrase=%s\n' "$password"''}
    } > /run/hostapd-${name}/base.conf
  '';

  start = name: h: import ./hotspot-start.nix {
    inherit pkgs lib name h;
    channel = channelOf h;
    customFile = customFile name h;
    inherit (w) countryCode;
  };

  service = name: h: {
    description = "Wi-Fi hotspot ${h.ssid} on ${name}";
    # Started by its adapter, at boot or whenever it is plugged in, and
    # stopped when it goes; a missing adapter holds up nothing else.
    wantedBy = [ (device name) ];
    bindsTo = [ (device name) ];
    after = [ (device name) ];
    serviceConfig = {
      ExecStartPre = prepare name h;
      # Picks the width and starts hostapd, which then runs in the background.
      Type = "forking";
      ExecStart = start name h;
      PIDFile = "/run/hostapd-${name}/hostapd.pid";
      ExecReload = "${pkgs.coreutils}/bin/kill -HUP $MAINPID";
      Restart = "on-failure";
      # Its own: systemd deletes a runtime directory when its service stops.
      RuntimeDirectory = "hostapd-${name}";

      # As NixOS's hostapd service.
      LockPersonality = true;
      MemoryDenyWriteExecute = true;
      DevicePolicy = "closed";
      DeviceAllow = "/dev/rfkill rw";
      NoNewPrivileges = true;
      PrivateUsers = false; # hostapd needs real root.
      PrivateTmp = false; # hostapd_cli opens a socket in /tmp.
      ProtectClock = true;
      ProtectControlGroups = true;
      ProtectHome = true;
      ProtectHostname = true;
      ProtectKernelLogs = true;
      ProtectKernelModules = true;
      ProtectKernelTunables = true;
      ProtectProc = "invisible";
      ProcSubset = "pid";
      ProtectSystem = "strict";
      RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_NETLINK" "AF_UNIX" "AF_PACKET" ];
      RestrictNamespaces = true;
      RestrictRealtime = true;
      RestrictSUIDSGID = true;
      SystemCallArchitectures = "native";
      SystemCallFilter = [ "@system-service" "~@privileged" "@chown" ];
      UMask = "0077";
    };
  };

  network = name: h: {
    matchConfig.Name = name;
    address = [ h.address ];
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

  # Overlapping subnets, checked on the addresses as numbers.
  ipToInt = s: lib.foldl (acc: o: acc * 256 + lib.toInt o) 0 (lib.splitString "." s);
  pow2 = n: lib.foldl (x: _: x * 2) 1 (lib.range 1 n);
  span = a:
    let
      parts = lib.splitString "/" a;
      size = pow2 (32 - lib.toInt (lib.elemAt parts 1));
      ip = ipToInt (lib.head parts);
      start = ip - lib.mod ip size;
    in { inherit start; end = start + size - 1; };
  pairs = l: lib.concatLists (lib.imap0 (i: a: map (b: [ a b ]) (lib.drop (i + 1) l)) l);
  activeList = lib.mapAttrsToList (name: h: h // { inherit name; }) active;
  overlapping = lib.filter (p:
    let a = span (lib.elemAt p 0).address; b = span (lib.elemAt p 1).address;
    in a.start <= b.end && b.start <= a.end) (pairs activeList);
  sameSsid = lib.filter (p: (lib.elemAt p 0).ssid == (lib.elemAt p 1).ssid) (pairs activeList);
  names = p: "${(lib.elemAt p 0).name} and ${(lib.elemAt p 1).name}";

  # Keys the service depends on: its adapter and its control socket.
  owned = [ "interface" "ctrl_interface" ];
  ownedIn = h: lib.intersectLists owned (lib.attrNames h.settings)
    ++ lib.filter (k: builtins.match "(.*\n)?[[:space:]]*${k}=.*" h.extraConfig != null) owned;

  odd = lib.filter (n: builtins.match "wl[upsx][0-9a-f]{12}" n == null) (lib.attrNames hotspots);
  host = config.conf.host.name;
in {
  config = lib.mkMerge [
    (lib.mkIf on {
      systemd.services = lib.mapAttrs' (name: h: lib.nameValuePair "hostapd-${name}" (service name h)) active;
      systemd.network.networks = lib.mapAttrs' (name: h: lib.nameValuePair "40-hotspot-${name}" (network name h)) active;
      systemd.network.config.networkConfig.IPv4Forwarding = true;
      networking.firewall.interfaces = lib.mapAttrs (_: _: { allowedUDPPorts = [ 67 ]; }) active;

      # Each hotspot is its own network: its devices go out to the uplink (any
      # interface that is not a hotspot), never to another hotspot's devices.
      # Answers come back through connection tracking; connections started
      # from the uplink side towards hotspot devices are dropped. Devices on
      # the same hotspot reach each other through the access point itself,
      # which this never sees.
      networking.firewall.filterForward = true;
      networking.firewall.extraForwardRules = lib.concatMapStrings (name: ''
        iifname "${name}" oifname != ${nftSet (lib.attrNames active)} accept comment "hotspot ${name} to the uplink"
      '') (lib.attrNames active);

      # Devices on a hotspot reach this machine only for DHCP, unless its
      # hostAccess is on. The firewall's own input rules cannot say "except on
      # these interfaces": its open ports come first. A chain checked before
      # them can, as a drop in any nftables chain is final.
      networking.nftables.tables.hotspot-guard = lib.mkIf (guarded != [ ]) {
        family = "inet";
        content = ''
          chain input {
            type filter hook input priority filter - 1; policy accept;
            iifname ${nftSet guarded} ct state established,related accept
            iifname ${nftSet guarded} udp dport 67 accept comment "DHCP"
            iifname ${nftSet guarded} drop
          }
        '';
      };

      hardware.wirelessRegulatoryDatabase = true;
      # The country from boot on, before anything sets it: the search for the
      # widest channel reads the rules in force (hotspot-start.nix).
      boot.kernelParams = lib.optional (w.countryCode != null)
        "cfg80211.ieee80211_regdom=${w.countryCode}";
      environment.systemPackages = [ pkgs.hostapd ];
    })

    {
      assertions =
        [
          {
            assertion = hotspots == { } || !w.wifi.enable || w.wifi.interfaces != { };
            message = "conf.networking.wireless.hotspots on '${host}': name the Wi-Fi client's interfaces in conf.networking.wireless.wifi.interfaces, so the client never takes a hotspot's adapter.";
          }
          {
            assertion = lib.intersectLists (lib.attrNames hotspots) (lib.attrNames w.wifi.interfaces) == [ ];
            message = "conf.networking.wireless on '${host}': ${lib.concatStringsSep ", " (lib.intersectLists (lib.attrNames hotspots) (lib.attrNames w.wifi.interfaces))} is both a Wi-Fi client interface and a hotspot.";
          }
          {
            assertion = overlapping == [ ];
            message = "conf.networking.wireless.hotspots on '${host}': the subnets of ${lib.concatMapStringsSep "; " names overlapping} overlap.";
          }
          {
            assertion = sameSsid == [ ];
            message = "conf.networking.wireless.hotspots on '${host}': ${lib.concatMapStringsSep "; " names sameSsid} show the same SSID; give each its own.";
          }
        ]
        ++ lib.concatLists (lib.mapAttrsToList (name: h: [
          {
            assertion = lib.stringLength name <= 15;
            message = "conf.networking.wireless.hotspots.${name} on '${host}': interface names are at most 15 characters.";
          }
          {
            assertion = ownedIn h == [ ];
            message = "conf.networking.wireless.hotspots.${name} on '${host}': settings and extraConfig cannot set ${lib.concatStringsSep " or " (ownedIn h)}; the hotspot's service is tied to them.";
          }
        ]) hotspots)
        ++ lib.concatLists (lib.mapAttrsToList (name: h: [
          {
            assertion = !cfg.enable || cfg.backend == "networkd";
            message = "conf.networking.wireless.hotspots.${name} on '${host}' needs conf.networking.backend = \"networkd\".";
          }
          {
            assertion = w.countryCode != null;
            message = "conf.networking.wireless.hotspots.${name} on '${host}' needs conf.networking.wireless.countryCode; the worldwide rules allow no hotspot on most channels.";
          }
          {
            assertion = !(h.band == "6g" && h.security != "wpa3");
            message = "conf.networking.wireless.hotspots.${name} on '${host}': 6 GHz allows WPA3 only; set security = \"wpa3\".";
          }
          {
            assertion = lib.stringLength h.ssid >= 1 && lib.stringLength h.ssid <= 32;
            message = "conf.networking.wireless.hotspots.${name}.ssid on '${host}' must be 1 to 32 bytes.";
          }
        ]) active);

      warnings = lib.optional (cfg.macNames && odd != [ ])
        ("conf.networking.wireless.hotspots on '${host}' names "
          + lib.concatStringsSep ", " odd + ", which conf.networking.macNames would not give a"
          + " Wi-Fi interface (wl, a bus letter, 12 hex digits). Fine for an adapter it leaves"
          + " alone; otherwise a leftover kernel name such as wlan0 that no longer exists.");
    }
  ];
}
