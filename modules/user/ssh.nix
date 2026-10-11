# Points the user's SSH client at the key the host provisions for them.
#
# The key lives under /run/secrets (tmpfs) rather than ~/.ssh: sops-nix would
# create a missing ~/.ssh as root, and tmpfs keeps a copy off the disk. The
# condition is false for standalone evaluations that were not given osConfig.
{ config, lib, osConfig ? null, ... }:
let
  hasHostKey = osConfig != null
    && (osConfig.conf.secrets.repo.url or null) != null
    && (osConfig.conf.users.accounts.${config.home.username}.secrets.sshKey or false);
in {
  config = lib.mkIf hasHostKey {
    programs.ssh = {
      enable = lib.mkDefault true;
      # Home Manager's implicit defaults are on their way out and it warns
      # while they are on; OpenSSH's own defaults are the same values.
      enableDefaultConfig = lib.mkDefault false;
      # settings, not the deprecated matchBlocks; OpenSSH directive names.
      settings."*".IdentityFile = [ "/run/secrets/users/${config.home.username}/ssh_key" ];
    };
  };
}
