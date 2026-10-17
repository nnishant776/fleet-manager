# Routing and filtering for the home LAN (br-lan: Ethernet, plus the USB Wi-Fi
# AP with homeWifi = "usb") and the
# WireGuard tunnel (wg0) behind the uplink to the host.
#
#   LAN    -> uplink (internet, ISP LAN, the Pi's SSH)
#   tunnel -> uplink and LAN
#   uplink -> this VM: WireGuard only
#   LAN    -> tunnel: nothing (roaming devices are not reachable from home)
{ lib, ... }:
let
  a = import ../addresses.nix;
  w = a.wg;
in {
  systemd.network = {
    links."10-uplink" = {
      matchConfig.MACAddress = w.mac;
      linkConfig.Name = "uplink";
    };
    links."10-lan-eth" = {
      matchConfig.MACAddress = w.lanMac;
      linkConfig.Name = "lan-eth";
    };

    networks."10-uplink" = {
      matchConfig.Name = "uplink";
      address = [ "${w.vmV4}/24" "${w.vmV6}/64" ];
      routes = [
        { Gateway = w.hostV4; }
        { Gateway = w.hostV6; }
      ];
      networkConfig.IPv6AcceptRA = false;
    };

    netdevs."20-br-lan".netdevConfig = {
      Kind = "bridge";
      Name = "br-lan";
    };
    networks."20-lan-eth" = {
      matchConfig.Name = "lan-eth";
      networkConfig.Bridge = "br-lan";
      linkConfig.RequiredForOnline = "no";
    };
    networks."20-br-lan" = {
      matchConfig.Name = "br-lan";
      address = [ "${w.lanV4}/24" "${w.lanV6}/64" ];
      networkConfig = {
        ConfigureWithoutCarrier = true;
        DHCPServer = true;
        IPv6SendRA = true;
      };
      dhcpServerConfig = {
        PoolOffset = 100;
        PoolSize = 100;
        EmitDNS = true;
        DNS = [ w.lanV4 ];
      };
      ipv6SendRAConfig = {
        EmitDNS = true;
        DNS = [ w.lanV6 ];
      };
      ipv6Prefixes = [{ Prefix = w.lanV6Prefix; }];
      linkConfig.RequiredForOnline = "no";
    };
  };

  # DNS for the LAN and the tunnel: resolved's stub, upstream as in common.nix.
  services.resolved.settings.Resolve.DNSStubListenerExtra = [
    w.lanV4
    w.lanV6
    w.tunnelV4
    w.tunnelV6
  ];

  boot.kernel.sysctl = {
    "net.ipv4.ip_forward" = 1;
    "net.ipv6.conf.all.forwarding" = 1;
  };

  networking.nftables.enable = true;
  networking.firewall = {
    enable = true;
    interfaces.uplink.allowedUDPPorts = [ w.port ];
    interfaces.br-lan = {
      allowedTCPPorts = [ 22 53 ];
      allowedUDPPorts = [ 53 67 ];
    };
    interfaces.wg0 = {
      allowedTCPPorts = [ 22 53 ];
      allowedUDPPorts = [ 53 ];
    };
  };

  networking.nftables.tables.wg-router = {
    family = "inet";
    content = ''
      chain forward {
        type filter hook forward priority filter; policy drop;
        ct state invalid drop
        ct state { established, related } accept

        iifname "br-lan" oifname "uplink" accept
        iifname "wg0" oifname { "uplink", "br-lan" } accept
      }

      chain postrouting {
        type nat hook postrouting priority srcnat;
        oifname "uplink" masquerade
      }
    '';
  };
}
