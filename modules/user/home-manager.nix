{ config, lib, pkgs, inputs, fleetUsersDir, ... }:
let
  usersCfg = config.conf.users;

  # Organisation-managed homes, activated by the system on rebuild. Users
  # with selfManagedHome are excluded here and get a published
  # homeConfigurations entry instead (lib/mkUser.nix): the two sets must stay
  # disjoint, since two Home Manager generations activating the same home
  # directory will delete each other's files.
  #
  # conf.users.manageHomes is the host-level switch: with it off, nothing
  # here manages any home and nothing is published either.
  hmUsers = lib.filterAttrs
    (_: u: usersCfg.manageHomes && !u.selfManagedHome)
    usersCfg.accounts;

  ungrantable = lib.filterAttrs
    (_: u: u.selfManagedHome && !usersCfg.manageHomes)
    usersCfg.accounts;
  buildUser = import ../../lib/buildUser.nix;
  flakeLib = import ../../lib/flakeLib.nix { inherit lib; };
in {
  home-manager = {
    useGlobalPkgs = true;
    useUserPackages = true;
    extraSpecialArgs = { inherit inputs flakeLib; };

    # Tagged with the flake revision so a second conflict on the same file
    # doesn't fail: a static extension would make that backup clash too.
    backupFileExtension = "bak-${inputs.self.shortRev or inputs.self.dirtyShortRev or "unknown"}";
    users = lib.mapAttrs (username: u:
      buildUser {
        inherit pkgs lib username;
        usersDir = fleetUsersDir;
        extraModules = [ u.homeConfig ];
        # allowLocalOverride stays at its default of false: this tree is
        # evaluated by root during a system rebuild, so the result must not
        # depend on a user-writable file outside the flake.
      }
    ) hmUsers;
  };

  warnings = lib.mapAttrsToList (username: _:
    "conf.users.accounts.${username} has selfManagedHome = true but"
    + " conf.users.manageHomes is false, so the grant has no effect —"
    + " nothing manages that home and nothing is published for them."
  ) ungrantable;
}
