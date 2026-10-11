# Installs the host side of the secrets design (lib/secrets-sync.nix) and
# makes sure the state directory exists, root-only, even where it is not a
# volume of its own.
{ config, lib, pkgs, ... }:
let
  scripts = import ../../../lib/secrets-sync.nix {
    inherit pkgs;
    nix = config.nix.package;
  };
in
{
  config = lib.mkIf (config.conf.secrets.repo.url != null && config.conf.platform == "nixos") {
    environment.systemPackages = [ scripts.sync scripts.identity ];

    systemd.tmpfiles.rules = [ "d ${config.conf.secrets.statePath} 0700 root root -" ];
  };
}
