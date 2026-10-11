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
    assertions = [
      {
        assertion = config.conf.fleet.signing.allowedSignersFile != null;
        message =
          "conf.secrets.repo.url is set but conf.fleet.signing.allowedSignersFile"
          + " is null: every secrets fetch verifies the commit against it, and"
          + " would fail without one.";
      }
      {
        # The sync authenticates with the host's SSH deploy key only, so an
        # HTTP(S) URL could never fetch the private repository.
        assertion = builtins.match "https?://.*" config.conf.secrets.repo.url == null;
        message =
          "conf.secrets.repo.url is an HTTP(S) URL, but the host fetches the"
          + " secrets repository with its SSH deploy key. Use an SSH URL"
          + " (git@host:org/repo.git or ssh://…).";
      }
      {
        # fleet-bootstrap and config-auto-update are what refresh the secrets;
        # both exist only on a host with a configuration upstream.
        assertion = config.conf.fleet.repo.url != null;
        message =
          "conf.secrets.repo.url is set but conf.fleet.repo.url is null: the"
          + " secrets are refreshed by the fleet sync, so without it they are"
          + " fetched once at install and never again.";
      }
    ];

    environment.systemPackages = [ scripts.sync scripts.identity ];

    systemd.tmpfiles.rules = [ "d ${config.conf.secrets.statePath} 0700 root root -" ];
  };
}
