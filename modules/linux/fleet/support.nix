# The maintainer's way into a fleet host: an account that needs no per-host
# secret, only the maintainer's public keys.
{ config, lib, ... }:
let
  cfg = config.conf.fleet.support;

  # null means "on if this host has opted in to secrets", so an opted-in host
  # is always reachable by the maintainer and a standalone machine is left
  # alone. An explicit value wins either way.
  enabled =
    if cfg.enable != null then cfg.enable else config.conf.secrets.repo.url != null;
in
{
  config = lib.mkIf (enabled && config.conf.platform == "nixos") {
    assertions = [
      {
        assertion = cfg.sshKeys != [ ];
        message =
          "conf.fleet.support is enabled but conf.fleet.support.sshKeys is empty:"
          + " the account's password is locked, so without a key nobody can use it.";
      }
      {
        assertion = !cfg.consolePassword || config.conf.secrets.repo.url != null;
        message =
          "conf.fleet.support.consolePassword is set but conf.secrets.repo.url is"
          + " null; the hash has to come from the host's file in the secrets repository.";
      }
    ];

    # Privileged, so wheel and ssh-users follow from host/privilege.nix. It
    # owns no secrets of its own: the key is the authentication factor.
    conf.users.accounts.${cfg.user} = {
      fullName = "Fleet support";
      privileged = true;
      sshKeys = cfg.sshKeys;
      secrets = {
        password = false;
        sshKey = false;
      };
    };

    # Without a password for this account only: its password is locked, so a
    # prompt could never be answered. Agent forwarding is kept off on the
    # maintainer's side.
    security.sudo.extraRules = [
      {
        users = [ cfg.user ];
        commands = [
          { command = "ALL"; options = [ "NOPASSWD" ]; }
        ];
      }
    ];

    # Plain priority, so it beats the role tables' mkDefault: the account is
    # useless on a host that does not run sshd.
    conf.sharing.ssh.enable = true;

    # Console login for when there is no network. neededForUsers puts the hash
    # in place before accounts are created.
    sops.secrets.support_password = lib.mkIf cfg.consolePassword {
      key = "support_password";
      neededForUsers = true;
    };

    users.users.${cfg.user}.hashedPasswordFile = lib.mkIf cfg.consolePassword
      config.sops.secrets.support_password.path;
  };
}
