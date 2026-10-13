# What the machine physically is, orthogonal to conf.role. Each type is a set
# of conf.* defaults applied with flakeLib.mkDefaults, so a host's own values
# win; the NixOS-level consequences (thermald, guest agents, the headless
# posture) live in the Linux modules and key off this option directly.
{ config, lib, flakeLib, ... }:
let
  headlessRoles = [ "server" "minimal" "embedded" ];

  machines = {
    laptop = {
      networking.wireless = {
        wifi.enable = true;
        bluetooth.enable = true;
      };
      hardware.powerManagement.enable = true;
    };
    desktop = {
      hardware.powerManagement.enable = true;
    };
    headless = { };
    vm = { };
  };
in {
  options.conf.machineType = lib.mkOption {
    type = lib.types.enum (builtins.attrNames machines);
    default = if builtins.elem config.conf.role headlessRoles then "headless" else "desktop";
    defaultText = lib.literalMD ''`"headless"` for the server, minimal and embedded roles, otherwise `"desktop"`'';
    description = ''
      Form factor of this machine. `laptop` adds Wi-Fi, Bluetooth and power
      management and keeps hibernation; `desktop` adds power management;
      `headless` is a server or appliance with no display and takes the
      hardened kernel posture; `vm` is a guest and gets the guest agents.
      Deduced from `conf.role` and overridable.
    '';
  };

  config = lib.mkMerge (lib.mapAttrsToList
    (name: defs: lib.mkIf (config.conf.machineType == name) { conf = flakeLib.mkDefaults defs; })
    machines);
}
