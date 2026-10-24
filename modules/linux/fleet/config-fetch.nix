# How the configuration fetch authenticates, shared by the sync (sync.nix) and
# the first-boot clone (bootstrap.nix). The URL decides:
#   https://…              a public repository: no key
#   git@host:… or ssh://…  a private one: the host's own configuration key,
#                          made with its identity (lib/secrets-sync.nix); the
#                          forge's host key is GitHub's published one
#                          (lib/forge-host-keys.nix), or for another forge the
#                          one first seen, in the known_hosts the secrets
#                          fetch uses too
# `ssh` says which; `line` is the shell line exporting GIT_SSH_COMMAND, or
# nothing.
{ config, lib }:
let
  url = config.conf.fleet.repo.url;
  state = config.conf.secrets.statePath;
  ssh = url != null && (lib.hasPrefix "ssh://" url || builtins.match "[^/:]+@[^/:]+:.*" url != null);
  pins = import ../../../lib/forge-host-keys.nix;
in {
  inherit ssh;
  line = lib.optionalString ssh ''
    export GIT_SSH_COMMAND=${lib.escapeShellArg "ssh -i ${state}/config_deploy_key -o IdentitiesOnly=yes ${pins.options} -o UserKnownHostsFile=${state}/known_hosts -o StrictHostKeyChecking=accept-new"}
  '';
}
