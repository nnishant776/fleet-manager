# Evaluates a host that has prepared a hotspot (hotspot.adapter set, enable
# false) with the hotspot turned on, without building anything, so the "on"
# variant cannot rot while it is off: turning it on stays a one-line change.
# flake.nix adds one such check per prepared host, `<host>-hotspot-ready`.
{ pkgs, name, host }:
let
  on = host.extendModules {
    modules = [{ conf.networking.wireless.hotspot.enable = pkgs.lib.mkForce true; }];
  };
in
pkgs.writeText "${name}-hotspot-ready"
  (builtins.unsafeDiscardStringContext on.config.system.build.toplevel.drvPath)
