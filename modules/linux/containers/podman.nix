{ lib, config, ... }:
let
  cfg = config.conf.containers;
in {
  config = lib.mkIf cfg.enable {
    virtualisation.containers.enable = true;
    virtualisation.containers.registries.search = cfg.registries;
    # Images come only from the registries above. This governs podman, skopeo
    # and buildah; Docker has no equivalent short of content trust.
    virtualisation.containers.policy = {
      default = [ { type = "reject"; } ];
      transports = {
        docker = lib.genAttrs cfg.registries (_: [ { type = "insecureAcceptAnything"; } ]);
        docker-daemon."" = [ { type = "insecureAcceptAnything"; } ];
        containers-storage."" = [ { type = "insecureAcceptAnything"; } ];
        dir."" = [ { type = "insecureAcceptAnything"; } ];
        oci."" = [ { type = "insecureAcceptAnything"; } ];
      };
    };
    virtualisation.podman = {
      enable = true;
      defaultNetwork.settings.dns_enabled = true;
    };

    # Forwarding is denied unless allowed (see docker.nix); podman's networks
    # (podman0, podman1, ...) are allowed out, as Docker's are.
    networking.firewall.filterForward = true;
    networking.firewall.extraForwardRules = ''
      iifname "podman*" accept comment "podman's networks"
    '';
  };
}
