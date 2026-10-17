{ lib, config, ... }:
let
  cfg = config.conf.containers;
in {
  config = lib.mkIf cfg.enable {
    virtualisation.docker = {
      enable = true;
      rootless = {
        enable = true;
        setSocketVariable = true;
      };
      daemon.settings = {
        # Containers on the default bridge do not see each other; user-defined
        # networks (compose, k3d) are unaffected.
        icc = lib.mkDefault false;
        "no-new-privileges" = lib.mkDefault true;
        # Containers survive a daemon restart, which every sync causes.
        "live-restore" = lib.mkDefault true;
      };
    };

    # NixOS turns IPv4 forwarding on for Docker at boot, and Docker sets a
    # drop policy for forwarded traffic only when it turns forwarding on
    # itself; so on its own, a Docker host routes between all its networks.
    # Forwarding is denied unless allowed, and containers' traffic is allowed:
    # the default network and user networks (br-<id>: compose, k3d). Docker's
    # own rules still decide between containers, and published ports pass by
    # NixOS's rule for address-translated traffic.
    networking.firewall.filterForward = true;
    networking.firewall.extraForwardRules = ''
      iifname "docker0" accept comment "Docker's default network"
      iifname "br-*" accept comment "Docker's user networks"
    '';
  };
}
