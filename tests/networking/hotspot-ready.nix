# Evaluates a host that has declared hotspots but left some off, with all of
# them turned on, without building anything, so the "on" variant cannot rot
# while it is off: turning a hotspot on stays a one-line change.
# flake.nix adds one such check per such host, `<host>-hotspot-ready`.
{ pkgs, name, host }:
let
  on = host.extendModules {
    modules = [{
      conf.networking.wireless.hotspots = pkgs.lib.mapAttrs
        (_: _: { enable = pkgs.lib.mkForce true; })
        host.config.conf.networking.wireless.hotspots;
    }];
  };
in
pkgs.writeText "${name}-hotspot-ready"
  (builtins.unsafeDiscardStringContext on.config.system.build.toplevel.drvPath)
