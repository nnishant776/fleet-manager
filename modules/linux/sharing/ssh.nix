{ config, lib, ... }:
let
  cfg = config.conf.sharing;
  enabled = config.conf.networking.enable && cfg.ssh.enable;
  secretsHost = config.conf.secrets.repo.url != null;
  fromSecrets = secretsHost
    && lib.any (a: a.secrets.authorizedKeys) (lib.attrValues config.conf.users.accounts);
in {
  config = lib.mkIf enabled {
    services.openssh = lib.mkMerge [
      ({
        enable = true;
        ports = [ 22 ];
        settings = {
          PasswordAuthentication = lib.mkDefault false;
          KbdInteractiveAuthentication = lib.mkDefault false;
          PermitRootLogin = "no";
          # Only members of ssh-users may log in: privileged users are added
          # automatically (host/privilege.nix); anyone else opts in through
          # `groups`.
          AllowGroups = lib.mkDefault [ "ssh-users" ];
          MaxAuthTries = lib.mkDefault 3;
          LoginGraceTime = lib.mkDefault 30;
          ClientAliveInterval = lib.mkDefault 300;
          ClientAliveCountMax = lib.mkDefault 2;
        };
      })
      # Keys from the secrets repository (`secrets.authorizedKeys`): a 0400
      # file per account, the account's own since sshd opens it as the
      # account, read at each login. Always with %u, so a file only ever
      # admits its own account.
      (lib.mkIf fromSecrets {
        authorizedKeysFiles = [ "/run/secrets/users/%u/authorized_keys" ];
      })
      # On a host with secrets, those are the only login keys: not
      # ~/.ssh/authorized_keys, which anything running as the account could
      # add a key to. The other file NixOS lists, /etc/ssh/authorized_keys.d,
      # holds what users.users.<name>.openssh.authorizedKeys declares, and that
      # is refused on such a host (host/users.nix), so it stays empty.
      (lib.mkIf secretsHost {
        authorizedKeysInHomedir = false;
      })
      (if cfg.ssh.config != {} then cfg.ssh.config else {})
    ];

    # Brute-force protection only matters while passwords are accepted.
    services.sshguard.enable = lib.mkDefault (config.services.openssh.settings.PasswordAuthentication == true);

    warnings = lib.optional
      (config.conf.fleet.repo.url != null && config.services.openssh.settings.PasswordAuthentication == true)
      ("conf.sharing.ssh on '${config.conf.host.name}': password authentication is on for a"
        + " fleet host. Keys through conf.users.accounts.<name>.sshKeys are the intended path.");
  };
}
