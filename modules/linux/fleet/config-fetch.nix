# How the configuration fetch authenticates, shared by the sync (sync.nix) and
# the first-boot clone (bootstrap.nix): a shell line exporting GIT_SSH_COMMAND,
# or nothing for a repository anyone can read.
#   repo.hostKey          the host's own config_ed25519, made with its identity
#                         (lib/secrets-sync.nix); the forge's host key is pinned
#                         on first contact in the same known_hosts as the
#                         secrets fetch uses
#   repo.deployKeySecret  a key delivered in the host's secrets file
{ config, lib }:
let
  cfg = config.conf.fleet;
  state = config.conf.secrets.statePath;
in
if cfg.repo.hostKey then ''
  export GIT_SSH_COMMAND=${lib.escapeShellArg "ssh -i ${state}/config_ed25519 -o IdentitiesOnly=yes -o UserKnownHostsFile=${state}/known_hosts -o StrictHostKeyChecking=accept-new"}
''
else if cfg.repo.deployKeySecret != null then ''
  export GIT_SSH_COMMAND="ssh -i ${config.sops.secrets.${cfg.repo.deployKeySecret}.path} -o IdentitiesOnly=yes"
''
else ""
