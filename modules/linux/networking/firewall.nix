# The firewall is nftables on every Linux host, with no way back to iptables:
# hotspots need it to keep their networks apart, and one form across the
# flake is one form to reason about. conf.networking.firewall.enable is
# three-state: null leaves the NixOS firewall at its default, which is on;
# true or false sets it explicitly. Ports are never listed here by hand: the
# service that owns a port opens it through its own openFirewall.
#
# Forwarded traffic is denied unless allowed on hosts that forward on purpose.
# Each module that makes a host forward (containers, VMs, hotspots) turns that
# on and allows its own traffic; a host's own forwardRules do the same here.
{ config, lib, options, ... }:
let
  cfg = config.conf.networking;
  fw = cfg.firewall;
  nft = config.networking.nftables;
  headless = config.conf.machineType == "headless";

  # Tables NixOS and the daemons on these hosts create, whatever their family
  # (from each one's source). A host table of the same name would merge with
  # NixOS's, or be deleted together with a daemon's on every firewall reload.
  takenTables = [
    # NixOS's firewall and NAT.
    "nixos-fw"
    "nixos-nat"
    # Docker's rules, through iptables' nftables build.
    "filter"
    "nat"
    "mangle"
    "raw"
    "security"
    # podman (netavark), libvirt, and systemd-networkd's IPMasquerade=.
    "netavark"
    "libvirt_network"
    "io.systemd.nat"
    # This flake's own (networking/hotspot.nix).
    "hotspot-guard"
  ];
  clashes = lib.intersectLists (lib.attrNames fw.tables) takenTables;

  # The rule options are a host's own additions; a definition coming from
  # modules/ means a module uses them, where it should set the rules itself.
  # The file declaring an option is skipped: its default counts as a
  # definition too.
  modulesDir = toString ../..;
  fromModules = name:
    let opt = options.conf.networking.firewall.${name}; in
    lib.filter (d: lib.hasPrefix "${modulesDir}/" d.file && !lib.elem d.file opt.declarations)
      opt.definitionsWithLocations;
  moduleUses = lib.unique (map (d: d.file)
    (lib.concatMap fromModules [ "inputRules" "forwardRules" "tables" ]));
in {
  config = lib.mkMerge [
    {
      networking.nftables.enable = true;

      assertions = [
        {
          assertion = nft.enable;
          message = "networking.nftables.enable is off on '${config.conf.host.name}', but nftables is this flake's firewall; nothing may turn it back to iptables.";
        }
        {
          # NixOS turns flushRuleset on by itself once a ruleset is set, and a
          # flush on every reload wipes the tables Docker, podman, libvirt and
          # systemd-networkd keep there.
          assertion = nft.ruleset == "" && nft.rulesetFile == null && !nft.flushRuleset;
          message = "'${config.conf.host.name}' sets networking.nftables.ruleset, rulesetFile or flushRuleset. A flush on every firewall reload would wipe the tables of Docker, podman, libvirt and systemd-networkd; put rules in conf.networking.firewall.tables instead.";
        }
      ];
    }

    (lib.mkIf cfg.enable {
      networking.firewall = lib.mkMerge [
        (lib.mkIf (fw.enable != null) { enable = fw.enable; })
        { logRefusedConnections = lib.mkDefault headless; }
        fw.config
        {
          extraInputRules = fw.inputRules;
          extraForwardRules = fw.forwardRules;
          filterForward = lib.mkIf (fw.forwardRules != "") true;
        }
      ];

      networking.nftables.tables = lib.mapAttrs (_: t: { inherit (t) family content; }) fw.tables;

      assertions = [
        {
          assertion = !(config.conf.fleet.repo.url != null && fw.enable == false);
          message = "conf.networking.firewall.enable = false is not allowed on a fleet host (conf.fleet.repo.url is set).";
        }
        {
          assertion = !(fw.config ? extraCommands || fw.config ? extraStopCommands);
          message = "conf.networking.firewall.config on '${config.conf.host.name}' sets extraCommands or extraStopCommands, which are iptables rules; the firewall is nftables. Write the rules in nftables form in conf.networking.firewall.inputRules (or forwardRules, or tables).";
        }
        {
          assertion = clashes == [ ];
          message = "conf.networking.firewall.tables on '${config.conf.host.name}' names ${lib.concatStringsSep ", " clashes}, a table NixOS or a daemon already uses; pick another name.";
        }
        {
          assertion = moduleUses == [ ];
          message = "conf.networking.firewall.inputRules, forwardRules and tables are a host's own additions, but ${lib.concatStringsSep ", " moduleUses} sets them. A module sets the rules its feature needs itself (networking.firewall.*, networking.nftables.tables).";
        }
      ];
    })
  ];
}
