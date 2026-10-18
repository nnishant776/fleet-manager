# fleet, the guided command-line tool (tools/fleet), on the PATH of every
# account that is privileged on the system this home belongs to: the accounts
# that look after the fleet. A home evaluated without osConfig cannot tell,
# and goes without.
{ config, lib, pkgs, osConfig ? null, ... }:
let
  privileged = osConfig != null
    && (osConfig.conf.users.accounts.${config.home.username}.privileged or false);
in {
  home.packages = lib.mkIf privileged [ (pkgs.callPackage ../../tools/fleet/package.nix { }) ];
}
