# Test that a host with conf.fleet.logs.lokiUrl sends its journal to Loki.
# A `loki` node runs Loki with its stock single-node configuration; `host`
# is built from this flake's modules (tests/fleet/host). The checks:
# - Alloy runs on the host, and a line logged there arrives in Loki, labelled
#   with the host and the unit;
# - audit records arrive too, labelled with the audit transport.
# Run it with `nix build .#checks.x86_64-linux.fleet-logs`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "checkout"; hostDir = ./host; };
in
pkgs.testers.runNixOSTest {
  name = "fleet-logs";

  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.loki = { pkgs, ... }: {
    services.loki = {
      enable = true;
      configFile = "${pkgs.grafana-loki.src}/cmd/loki/loki-local-config.yaml";
    };
    networking.firewall.allowedTCPPorts = [ 3100 ];
    environment.systemPackages = [ pkgs.grafana-loki ]; # logcli
  };

  nodes.host = {
    imports = hostNode.modules ++ [{
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;
      conf.fleet.logs.lokiUrl = "http://loki:3100/loki/api/v1/push";
    }];
  };

  testScript = ''
    def found(query, text):
        return (
            f"logcli query --quiet --no-labels --since=1h '{query}'"
            f" | grep -q {text!r}"
        )

    start_all()
    loki.wait_for_unit("loki.service")
    loki.wait_for_open_port(3100)
    host.wait_for_unit("alloy.service")

    with subtest("a line logged on the host arrives, labelled with the host and the unit"):
        host.succeed("systemd-run --unit=fleet-logs-probe --wait echo 'fleet logs probe'")
        loki.wait_until_succeeds(
            found('{host="checkout", unit="fleet-logs-probe.service"}', "fleet logs probe"),
            timeout=120,
        )

    with subtest("audit records arrive, labelled with their transport"):
        # Writing /etc/passwd's watched path is enough for a record.
        host.succeed("touch /etc/passwd")
        loki.wait_until_succeeds(
            found('{host="checkout", transport="audit"}', "identity"),
            timeout=120,
        )
  '';
}
