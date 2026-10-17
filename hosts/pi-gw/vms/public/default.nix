# Public services VM. Its only way out is IPv4 to the internet (network.nix on
# the host), which is all cloudflared needs; there is no SSH and no login.
{ ... }:
let
  a = import ../addresses.nix;
in {
  imports = [
    ../common.nix
    ./tunnel.nix
  ];

  networking.hostName = "public";

  microvm = {
    vcpu = 1;
    mem = 1024;
    interfaces = [{
      type = "tap";
      id = a.public.tap;
      mac = a.public.mac;
      # No vhost-net: this VM's packets are handled by its own sandboxed QEMU,
      # not by host kernel code the guest can drive (docs/pi-gw-design.md, M3).
      tap.vhost = false;
    }];
    volumes = [{
      image = "var.img";
      mountPoint = "/var";
      size = 1024;
    }];
    credentialFiles.cloudflared-tunnel = "${(import ../names.nix).credentialsDir}/public/tunnel.json";
  };

  systemd.network.networks."10-uplink" = {
    matchConfig.MACAddress = a.public.mac;
    address = [ "${a.public.vmV4}/24" ];
    routes = [{ Gateway = a.public.hostV4; }];
    networkConfig = {
      LinkLocalAddressing = "no";
      IPv6AcceptRA = false;
    };
  };

  # Nothing listens for the outside; replies to cloudflared's own connections
  # are all that come in.
  networking.firewall.enable = true;
}
