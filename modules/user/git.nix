# Signs commits with the key the host provisions for the user, under the same
# condition as ssh.nix (the host declares secrets.sshKey for this user).
{ config, pkgs, lib, osConfig ? null, ... }:
let
  hasHostKey = osConfig != null
    && (osConfig.conf.secrets.repo.url or null) != null
    && (osConfig.conf.users.accounts.${config.home.username}.secrets.sshKey or false);
in {
  config = lib.mkIf hasHostKey {
    programs.git.signing = {
      format = "ssh";
      key = "/run/secrets/users/${config.home.username}/ssh_key";
      signByDefault = lib.mkDefault true;
    };
  };
}
