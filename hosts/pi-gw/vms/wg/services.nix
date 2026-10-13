# Private services, reachable from the LAN and the tunnel only. Each opens its
# port on br-lan and wg0, never on the uplink.
{ pkgs, lib, ... }:
let
  sandbox = import ../sandbox.nix;
  port = 8081;
in {
  # Placeholder: a static page. Replace ExecStart with the real service.
  systemd.services.private-web = {
    description = "Private web UI";
    wantedBy = [ "multi-user.target" ];
    serviceConfig = sandbox.serviceConfig // {
      ExecStart = lib.escapeShellArgs [
        "${pkgs.static-web-server}/bin/static-web-server"
        "--host" "::"
        "--port" (toString port)
        "--root" (pkgs.writeTextDir "index.html" "private is up\n")
      ];
      Restart = "on-failure";
      RestrictAddressFamilies = [ "AF_INET" "AF_INET6" ];
    };
  };

  networking.firewall.interfaces.br-lan.allowedTCPPorts = [ port ];
  networking.firewall.interfaces.wg0.allowedTCPPorts = [ port ];
}
