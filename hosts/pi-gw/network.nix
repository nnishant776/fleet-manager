# The Pi as a router between its Wi-Fi uplink and the VMs. A Wi-Fi client
# cannot be bridged (the ISP router drops frames from any MAC but the Pi's), so
# the VMs sit on routed taps behind NAT, and every rule about what may talk to
# what lives here, out of reach of the VMs:
#
#   public  -> internet over IPv4, never a private or link-local address (so
#              not the ISP router, the ISP LAN or the wg VM), no IPv6 at all
#   wg      -> anywhere upstream, the ISP router's admin page included
#   uplink  -> wg only for WireGuard, port-forwarded from the Pi's addresses
#   public <-> wg: nothing
#
# The Pi accepts SSH on the wg VM's link only. Nothing is open on the uplink or
# on the public VM's link.
{ lib, ... }:
let
  a = import ./vms/addresses.nix;
  up = a.host.uplink;
in {
  # NetworkManager keeps the Wi-Fi uplink, whose connection the first boot of
  # the rpi-image card writes; systemd-networkd owns the VM links.
  networking.networkmanager.unmanaged = [
    "interface-name:vm-*"
    "interface-name:${a.host.ethernet}"
  ];
  systemd.network = {
    enable = true;
    # network-online is NetworkManager's to report.
    wait-online.enable = false;

    networks."30-${a.public.tap}" = {
      matchConfig.Name = a.public.tap;
      address = [ "${a.public.hostV4}/24" ];
      networkConfig = {
        LinkLocalAddressing = "no";
        IPv6AcceptRA = false;
      };
      linkConfig.RequiredForOnline = "no";
    };

    networks."30-${a.wg.tap}" = {
      matchConfig.Name = a.wg.tap;
      address = [ "${a.wg.hostV4}/24" "${a.wg.hostV6}/64" ];
      networkConfig.IPv6AcceptRA = false;
      linkConfig.RequiredForOnline = "no";
    };

    # Lower device of the wg VM's macvtap: up, but carrying nothing of the Pi's.
    networks."30-${a.host.ethernet}" = {
      matchConfig.Name = a.host.ethernet;
      networkConfig = {
        LinkLocalAddressing = "no";
        IPv6AcceptRA = false;
      };
      linkConfig.RequiredForOnline = "no";
    };
  };

  # Routing. NetworkManager handles router advertisements in userspace, so the
  # uplink keeps its SLAAC addresses with forwarding on.
  boot.kernel.sysctl = {
    "net.ipv4.ip_forward" = 1;
    "net.ipv6.conf.all.forwarding" = 1;
  };

  networking.nftables.enable = true;
  networking.firewall.interfaces.${a.wg.tap}.allowedTCPPorts = [ 22 ];

  networking.nftables.tables.pi-gw = {
    family = "inet";
    content = ''
      # Everything that is not the public internet.
      set private4 {
        type ipv4_addr
        flags interval
        elements = {
          0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16,
          172.16.0.0/12, 192.0.0.0/24, 192.168.0.0/16, 198.18.0.0/15, 224.0.0.0/3
        }
      }

      chain forward {
        type filter hook forward priority filter; policy drop;
        ct state invalid drop
        ct state { established, related } accept

        iifname "${a.public.tap}" oifname "${up}" meta nfproto ipv4 ip daddr != @private4 accept
        iifname "${a.wg.tap}" oifname "${up}" accept
        iifname "${up}" oifname "${a.wg.tap}" ct status dnat udp dport ${toString a.wg.port} accept
      }

      chain prerouting {
        type nat hook prerouting priority dstnat;
        iifname "${up}" udp dport ${toString a.wg.port} dnat ip to ${a.wg.vmV4}
        iifname "${up}" udp dport ${toString a.wg.port} dnat ip6 to ${a.wg.vmV6}
      }

      chain postrouting {
        type nat hook postrouting priority srcnat;
        oifname "${up}" ip saddr { ${a.public.vmV4}, ${a.wg.vmV4} } masquerade
        oifname "${up}" ip6 saddr ${a.wg.vmV6} masquerade
      }
    '';
  };
}
