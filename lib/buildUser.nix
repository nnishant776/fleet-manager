# Canonical user environment module generator, used by lib/mkUser.nix.
#
# Layers, in the order they are imported:
#   1. modules/user/           organisation baseline (declares values with mkDefault)
#   2. users/<username>/       organisation per-user config, auto-discovered
#   3. extraModules            host-declared conf.users.accounts.<name>.homeConfig
#   4. ~/.config/home-manager  the user's own config — STANDALONE ONLY, never
#                              read during a system rebuild
{ pkgs
, lib ? pkgs.lib
, username
, extraModules ? [ ]
  # The fleet's folder of per-user configuration (lib.mkFleet's users), or null.
, usersDir ? null
  # Enables importing ~/.config/home-manager/home.nix. Only for standalone
  # Home Manager (lib/mkUser.nix); must stay off for anything the system
  # evaluates, since a rebuild runs as root and the file belongs to the user.
, allowLocalOverride ? false
}:
let
  homeDir =
    if pkgs.stdenv.isDarwin
    then "/Users/${username}"
    else "/home/${username}";

  # Organisation-defined config for this user, tracked in the flake.
  orgConfig = if usersDir == null then null else usersDir + "/${username}/default.nix";

  # Machine-local config owned by the user, outside the flake.
  localConfig = homeDir + "/.config/home-manager/home.nix";
in {
  imports =
    [ ../modules/user/default.nix ]
    ++ lib.optional (orgConfig != null && builtins.pathExists orgConfig) orgConfig
    ++ extraModules
    ++ lib.optional (allowLocalOverride && builtins.pathExists localConfig) (import localConfig);

  home = {
    username = lib.mkDefault username;
    homeDirectory = lib.mkDefault homeDir;
  };
}
