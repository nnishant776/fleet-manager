{ inputs }:
let
  lib = inputs.nixpkgs.lib;
  buildUser = import ./buildUser.nix;
  flakeLib = import ./flakeLib.nix { inherit lib; };

  mkUser =
    { username
      # The host's own package set, which every caller has: a home configuration
      # is only ever published for a user some host declares. Taking it rather
      # than instantiating nixpkgs again keeps a user's home on exactly the
      # packages, overlays and nixpkgs configuration of the machine it runs on.
    , pkgs
    , extraModules ? [ ]
    , usersDir ? null
      # Safe here since Home Manager is invoked by the user on their own
      # machine; buildUser defaults this off so a future system-level caller
      # has to opt in deliberately.
    , allowLocalOverride ? true
      # The host's evaluated configuration, handed to Home Manager modules as
      # `osConfig` just as the NixOS integration does, so a self-managed home
      # reaches the same answers as an organisation-managed one.
    , osConfig ? null
    }:
    inputs.home-manager.lib.homeManagerConfiguration {
      inherit pkgs;
      extraSpecialArgs = { inherit inputs flakeLib osConfig; };
      modules = [
        (buildUser {
          inherit pkgs lib username extraModules usersDir allowLocalOverride;
        })
      ];
    };

  # Every user whose home they manage themselves, across all hosts.
  hostUserEntries = { hostConfigs }:
    lib.flatten (lib.mapAttrsToList (dirName: hostCfg:
      let
        users = hostCfg.config.conf.users;
        # Only users with selfManagedHome. Organisation-managed homes are
        # activated by the system (modules/user/home-manager.nix) and must not
        # also be published here.
        enabledUsers = lib.filterAttrs
          (_: u: users.manageHomes && u.selfManagedHome)
          users.accounts;
      in
        lib.mapAttrsToList (username: u: {
          inherit username dirName;
          userCfg = u;
          pkgs = hostCfg.pkgs;
          osConfig = hostCfg.config;
        }) enabledUsers
    ) hostConfigs);

  entryToUser = usersDir: e:
    mkUser {
      inherit usersDir;
      inherit (e) username pkgs osConfig;
      extraModules = [ e.userCfg.homeConfig ];
    };

  # Keyed `<username>@<host-dir>`:
  #
  #   home-manager switch --flake /etc/nixos#<username>@<host>
  #
  # There is deliberately no bare `<username>` alias; the hostname is a
  # property of the machine running the command, so the `home-switch` wrapper
  # resolves it in the shell instead.
  mkHomeConfigurations = { hostConfigs, usersDir ? null }:
    lib.listToAttrs (map
      (e: lib.nameValuePair "${e.username}@${e.dirName}" (entryToUser usersDir e))
      (hostUserEntries { inherit hostConfigs; }));

in {
  inherit mkUser mkHomeConfigurations;
  __functor = self: self.mkUser;
}
