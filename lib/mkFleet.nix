# lib.mkFleet: a fleet's systems, homes and apps, from its folders of host and
# user definitions. fleet-manager calls it on its own example and test hosts.
# A private flake that pins fleet-manager calls it on the real ones:
#
#   inputs.fleet-manager.url = "github:nnishant776/fleet-manager";
#   outputs = { self, fleet-manager }: fleet-manager.lib.mkFleet {
#     inherit self;
#     hosts = ./config/hosts;
#     users = ./config/users;
#     extraSystems = [ "x86_64-linux" ];
#   };
#
# Modules see the calling flake as inputs.self: the revision a machine records
# and its homes are checked against is that flake's commit. Every other input
# (nixpkgs, home-manager, disko, …) is fleet-manager's, as it pins them.
{ inputs }:
{
  self,
  # One folder per host, named after it; see hosts/ in fleet-manager.
  hosts,
  # Per-user configuration, users/<user>/default.nix; null for none.
  users ? null,
  # Systems to offer the apps on besides the hosts', such as the
  # maintainer's machine and CI's: [ "x86_64-linux" ].
  extraSystems ? [ ],
  # fleet-manager's own utilities, which no fleet needs: rpi-image and
  # secrets-harness. Off for a private fleet, which runs them from
  # fleet-manager itself.
  libraryApps ? false,
}:
let
  lib = inputs.nixpkgs.lib;
  fleetInputs = inputs // { inherit self; };
  mkHost = import ./mkHost.nix { inputs = fleetInputs; usersDir = users; };
  mkUser = import ./mkUser.nix { inputs = fleetInputs; };

  hostNames = builtins.attrNames (
    lib.filterAttrs (_: t: t == "directory") (builtins.readDir hosts)
  );

  hostPlatforms = lib.genAttrs hostNames (h:
    let
      hostFile = import (hosts + "/${h}/default.nix");
      raw = if builtins.isFunction hostFile then hostFile { config = {}; pkgs = {}; lib = lib; } else hostFile;
    in
      raw.nixpkgs.hostPlatform
  );

  linuxHosts = lib.filterAttrs (_: p: (lib.systems.elaborate p).isLinux) hostPlatforms;
  darwinHosts = lib.filterAttrs (_: p: (lib.systems.elaborate p).isDarwin) hostPlatforms;

  nixosConfigurations = lib.mapAttrs (h: _: mkHost { hostName = h; hostDir = hosts + "/${h}"; }) linuxHosts;
  darwinConfigurations = lib.mapAttrs (h: _: mkHost { hostName = h; hostDir = hosts + "/${h}"; }) darwinHosts;

  # The two sets are keyed by host directory and a directory has one platform,
  # so they cannot overlap; unionOfDisjoint says so and fails loudly if that
  # ever stops being true, where // would silently keep one side.
  allHosts = lib.attrsets.unionOfDisjoint nixosConfigurations darwinConfigurations;

  homeConfigurations = mkUser.mkHomeConfigurations {
    hostConfigs = allHosts;
    usersDir = users;
  };

  # One set of apps per system any host uses; the scripts live in lib/apps.
  systems = lib.unique (lib.attrValues (lib.attrsets.unionOfDisjoint linuxHosts darwinHosts) ++ extraSystems);
  apps = lib.genAttrs systems (system:
    import ./apps {
      inherit self libraryApps;
      inputs = fleetInputs;
      pkgs = inputs.nixpkgs.legacyPackages.${system};
    });
in {
  inherit nixosConfigurations darwinConfigurations homeConfigurations apps;
}
