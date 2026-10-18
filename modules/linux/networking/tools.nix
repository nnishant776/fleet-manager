# Tools for looking at network interfaces on any host with networking: ethtool
# for wired links (speed, duplex, offloads, driver), and iw for radios
# (channels, widths and regulatory rules, which hotspots depend on).
{ config, pkgs, lib, ... }:
let
  cfg = config.conf.networking;
in {
  config = lib.mkIf cfg.enable {
    environment.systemPackages = [ pkgs.ethtool ]
      ++ lib.optional cfg.wireless.enable pkgs.iw;
  };
}
