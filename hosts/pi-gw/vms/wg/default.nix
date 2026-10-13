# Home router and VPN endpoint. Home devices join its LAN over the Pi's
# Ethernet port and, with homeWifi = "usb" in addresses.nix, a USB Wi-Fi AP
# (hostapd.nix); roaming devices come in over WireGuard. Its uplink is a routed
# tap to the host, which NATs it to the ISP router.
{ lib, ... }:
let
  a = import ../addresses.nix;
  w = a.wg;
in {
  imports = [
    ../common.nix
    ./network.nix
    ./wireguard.nix
    ./services.nix
  ] ++ lib.optional (w.homeWifi == "usb") ./hostapd.nix;

  networking.hostName = "wg";

  microvm = {
    vcpu = 2;
    mem = 1024;
    interfaces = [
      {
        type = "tap";
        id = w.tap;
        mac = w.mac;
        tap.vhost = true;
      }
      {
        # The Pi's Ethernet port, whole: the host keeps no address on it.
        type = "macvtap";
        id = w.lanTap;
        mac = w.lanMac;
        macvtap = {
          link = a.host.ethernet;
          mode = "passthru";
        };
      }
    ];
    volumes = [{
      image = "var.img";
      mountPoint = "/var";
      size = 1024;
    }];
  };

  # Key-only root login, from the LAN and the tunnel (firewall in network.nix).
  services.openssh = {
    enable = true;
    openFirewall = false;
    settings = {
      PermitRootLogin = "prohibit-password";
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
    };
  };
  users.users.root.openssh.authorizedKeys.keys = [
    # "ssh-ed25519 AAAA... admin@generic"
  ];
}
