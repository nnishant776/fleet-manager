# Public exposure through a Cloudflare Tunnel, and the containment around it.
# Adapted from hosts/edge/tunnel.nix; kept separate so edge can change freely.
#
# cloudflared dials out and forwards matching requests to the backend on
# localhost. Both units are sandboxed: cloudflared can reach the internet and
# localhost only, the backend nothing but its own loopback.
#
# Setup, once, from any machine with cloudflared:
#   cloudflared tunnel login
#   cloudflared tunnel create public           # prints the tunnel ID, writes <ID>.json
#   cloudflared tunnel route dns public <hostname>
# then on pi-gw put <ID>.json at /var/lib/microvm-secrets/public/tunnel.json
# (see microvms.nix) and fill in tunnelId and hostname.
{ pkgs, lib, ... }:
let
  sandbox = import ../sandbox.nix;

  tunnelId = "00000000-0000-0000-0000-000000000000";
  hostname = "app.example.com";
  backendPort = 8080;
in {
  services.cloudflared = {
    enable = true;
    tunnels.${tunnelId} = {
      # Passed in by QEMU as a systemd credential (default.nix).
      credentialsFile = "/run/credentials/@system/cloudflared-tunnel";
      edgeIPVersion = "4";
      ingress.${hostname} = "http://127.0.0.1:${toString backendPort}";
      default = "http_status:404";
    };
  };

  systemd.services."cloudflared-tunnel-${tunnelId}".serviceConfig = sandbox.serviceConfig // {
    IPAddressDeny = sandbox.privateRanges;
  };

  # Placeholder backend: a static page on localhost. Replace ExecStart with the
  # real service and keep the rest.
  systemd.services.public-backend = {
    description = "Backend served through the Cloudflare Tunnel";
    wantedBy = [ "multi-user.target" ];
    serviceConfig = sandbox.serviceConfig // {
      ExecStart = lib.escapeShellArgs [
        "${pkgs.static-web-server}/bin/static-web-server"
        "--host" "127.0.0.1"
        "--port" (toString backendPort)
        "--root" (pkgs.writeTextDir "index.html" "public is up\n")
      ];
      Restart = "on-failure";
      IPAddressAllow = [ "localhost" ];
      IPAddressDeny = [ "any" ];
      RestrictAddressFamilies = [ "AF_INET" "AF_INET6" ];
    };
  };
}
