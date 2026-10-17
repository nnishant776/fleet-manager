# WireGuard endpoint for roaming devices. The host forwards UDP on its uplink
# addresses here; ddns.nix keeps the DNS name on the host's stable IPv6.
#
# The private key is made on first boot and kept on the /var volume; the public
# key for client configs is at /var/lib/wireguard/public.key. A client:
#   [Interface]
#   PrivateKey = <client key>
#   Address = 10.10.100.2/32, fd5e:2c8a:91d4:100::2/128
#   DNS = 10.10.100.1
#   [Peer]
#   PublicKey = <this VM's public.key>
#   Endpoint = private.geeky-panda.dev:51820
#   AllowedIPs = 0.0.0.0/0, ::/0     # or only 192.168.50.0/24, fd5e:2c8a:91d4::/48
{ pkgs, ... }:
let
  a = import ../addresses.nix;
  w = a.wg;

  peers = [
    # {
    #   PublicKey = "<client public key>";
    #   AllowedIPs = [ "10.10.100.2/32" "${a.ula}:100::2/128" ];
    # }
  ];
in {
  systemd.services.wireguard-key = {
    description = "Generate the WireGuard private key";
    wantedBy = [ "systemd-networkd.service" ];
    before = [ "systemd-networkd.service" ];
    unitConfig = {
      RequiresMountsFor = "/var/lib";
      ConditionPathExists = "!/var/lib/wireguard/private.key";
    };
    serviceConfig.Type = "oneshot";
    path = [ pkgs.wireguard-tools pkgs.coreutils ];
    script = ''
      install -d -m 0750 -g systemd-network /var/lib/wireguard
      umask 0137
      wg genkey > /var/lib/wireguard/private.key.new
      chgrp systemd-network /var/lib/wireguard/private.key.new
      wg pubkey < /var/lib/wireguard/private.key.new > /var/lib/wireguard/public.key
      chmod 0644 /var/lib/wireguard/public.key
      mv /var/lib/wireguard/private.key.new /var/lib/wireguard/private.key
    '';
  };

  systemd.network.netdevs."30-wg0" = {
    netdevConfig = {
      Kind = "wireguard";
      Name = "wg0";
    };
    wireguardConfig = {
      ListenPort = w.port;
      PrivateKeyFile = "/var/lib/wireguard/private.key";
    };
    wireguardPeers = peers;
  };
  systemd.network.networks."30-wg0" = {
    matchConfig.Name = "wg0";
    address = [ "${w.tunnelV4}/24" "${w.tunnelV6}/64" ];
  };

  environment.systemPackages = [ pkgs.wireguard-tools ];
}
