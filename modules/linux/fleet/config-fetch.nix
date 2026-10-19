# How the configuration fetch authenticates, shared by the sync (sync.nix) and
# the first-boot clone (bootstrap.nix). The URL decides:
#   https://…              a public repository: no key
#   git@host:… or ssh://…  a private one: the host's own configuration key,
#                          made with its identity (lib/secrets-sync.nix); the
#                          forge's host key is pinned on first contact, in the
#                          same known_hosts as the secrets fetch uses
# `ssh` says which; `line` is the shell line exporting GIT_SSH_COMMAND, or
# nothing.
{ config, lib }:
let
  url = config.conf.fleet.repo.url;
  state = config.conf.secrets.statePath;
  ssh = url != null && (lib.hasPrefix "ssh://" url || builtins.match "[^/:]+@[^/:]+:.*" url != null);
in {
  inherit ssh;
  line = lib.optionalString ssh ''
    export GIT_SSH_COMMAND=${lib.escapeShellArg "ssh -i ${state}/config_ed25519 -o IdentitiesOnly=yes -o UserKnownHostsFile=${state}/known_hosts -o StrictHostKeyChecking=accept-new"}
  '';
}
