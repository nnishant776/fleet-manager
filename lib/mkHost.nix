# usersDir is the fleet's folder of per-user configuration, or null; modules get it
# as fleetUsersDir, for the homes the system builds.
{ inputs, usersDir ? null }:
let
  lib = inputs.nixpkgs.lib;
  flakeLib = import ./flakeLib.nix { inherit lib; };

  # Every .nix file in the host directory is a module: default.nix,
  # hardware-configuration.nix, disko-config.nix, network.nix and whatever else
  # a host keeps there. default.nix never has to import its siblings.
  hostModulesOf = hostDir:
    let entries = builtins.readDir hostDir;
    in map (f: hostDir + "/${f}")
      (builtins.filter (f: entries.${f} == "regular" && lib.hasSuffix ".nix" f)
        (builtins.attrNames entries));

  # The hosts/ directory name and conf.host.name must match: both
  # `nixos-rebuild switch` (resolving #$(hostname)) and the <user>@<host-dir>
  # home configuration keys depend on it.
  hostIdentity = hostName: { config, ... }: {
    assertions = [
      {
        assertion = config.conf.host.name == hostName;
        message =
          "hosts/${hostName} declares conf.host.name = \"${config.conf.host.name}\"."
          + " The host directory name and conf.host.name must be identical:"
          + " `nixos-rebuild switch` resolves #$(hostname) and home"
          + " configurations are keyed <user>@${hostName}."
          + " Either rename the directory to hosts/${config.conf.host.name}"
          + " or set conf.host.name = \"${hostName}\".";
      }
    ];
  };

  # Home Manager is wired in here for organisation-managed homes. Users with
  # conf.users.accounts.<name>.selfManagedHome are excluded and get a published
  # homeConfigurations entry instead.
  commonModulesFor = { hostName, hostDir }: hostModulesOf hostDir ++ [
    (hostIdentity hostName)
    ../modules/conf
    ../modules/common
    ../modules/user/home-manager.nix
  ];

  # The module list and specialArgs of a Linux host, split out of mkHost so a
  # NixOS VM test can build a node from a host directory.
  modulesFor = args: {
    modules = commonModulesFor args ++ [
      {
        system.stateVersion = "26.05";
        conf.platform = "nixos";
      }
      ../modules/linux
      inputs.home-manager.nixosModules.home-manager
      inputs.noctalia.nixosModules.default
      inputs.disko.nixosModules.disko
      inputs.sops-nix.nixosModules.sops
      inputs.dms.nixosModules.dank-material-shell
    ];
    specialArgs = { inherit inputs flakeLib; fleetUsersDir = usersDir; };
  };

  mkHost = { hostName, hostDir }:
  let
    hostFile = import (hostDir + "/default.nix");
    rawHost = if builtins.isFunction hostFile then hostFile { config = {}; pkgs = {}; lib = lib; } else hostFile;
    system = rawHost.nixpkgs.hostPlatform;
    sysElaborate = lib.systems.elaborate system;
    isLinux = sysElaborate.isLinux;
    isDarwin = sysElaborate.isDarwin;
  in
    if isLinux then
      inputs.nixpkgs.lib.nixosSystem (modulesFor { inherit hostName hostDir; })
    else if isDarwin then
      inputs.nix-darwin.lib.darwinSystem {
        modules = commonModulesFor { inherit hostName hostDir; } ++ [
          { conf.platform = "darwin"; }
          ../modules/darwin
          inputs.home-manager.darwinModules.home-manager
          inputs.sops-nix.darwinModules.sops
        ];
        specialArgs = { inherit inputs flakeLib; fleetUsersDir = usersDir; };
      }
    else
      builtins.throw "mkHost: unsupported system '${system}' for host '${hostName}'";
in {
  inherit modulesFor;
  __functor = self: mkHost;
}
