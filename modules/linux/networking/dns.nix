# Resolver posture for networks the machine does not trust: systemd-resolved
# with opportunistic DNS-over-TLS and DNSSEC that downgrades rather than
# breaks, so captive portals still work. NetworkManager or systemd-networkd,
# whichever is the backend, hands its servers to resolved.
{ config, lib, ... }:
{
  config = lib.mkIf config.conf.networking.enable {
    services.resolved = {
      enable = lib.mkDefault true;
      settings.Resolve = {
        DNSOverTLS = lib.mkDefault "opportunistic";
        DNSSEC = lib.mkDefault "allow-downgrade";
      };
    };
    networking.networkmanager.dns = lib.mkIf (config.conf.networking.backend == "networkmanager")
      (lib.mkDefault "systemd-resolved");
  };
}
