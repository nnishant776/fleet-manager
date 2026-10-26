{ config, lib, ... }:
{
  config = {
    # On a host with secrets, SSH keys live in one place: the secrets
    # repository. The options stay for hosts without one. NixOS's own
    # users.users.<name>.openssh.authorizedKeys, which any module or host file
    # could set, for any account root included, are refused there too: sshd
    # reads them from /etc/ssh/authorized_keys.d.
    assertions = lib.optionals (config.conf.secrets.repo.url != null)
      ((lib.mapAttrsToList (username: user: {
        assertion = user.openssh.authorizedKeys.keys == [ ] && user.openssh.authorizedKeys.keyFiles == [ ];
        message = "users.users.${username}.openssh.authorizedKeys is set on a host with secrets;"
          + " login keys come only from the account's file in the secrets repository"
          + " (conf.users.accounts.<name>.secrets.authorizedKeys, set with"
          + " `fleet secrets user-keys <name> <host>`).";
      }) config.users.users)
      ++ lib.concatLists (lib.mapAttrsToList (username: user: [
        {
          assertion = user.sshKeys == [ ];
          message = "conf.users.accounts.${username} lists sshKeys on a host with secrets;"
            + " the keys go in the account's file in the secrets repository, with"
            + " secrets.authorizedKeys, set with `fleet secrets user-keys ${username} <host>`.";
        }
        {
          assertion = user.sudoSshKeys == [ ];
          message = "conf.users.accounts.${username} lists sudoSshKeys on a host with secrets;"
            + " the keys go in the account's file in the secrets repository, with"
            + " secrets.sudoKeys, set with `fleet secrets user-keys ${username} <host>`.";
        }
      ]) config.conf.users.accounts));

    users.users = lib.mkMerge [
      (lib.mapAttrs (username: user: {
        isNormalUser = true;
        description = user.fullName;
        # null leaves the account locked until a key or an administrator opens it.
        initialHashedPassword = user.initialHashedPassword;
        openssh.authorizedKeys.keys = user.sshKeys;
      }) config.conf.users.accounts)

      # root is reached through sudo only. Unlike initialHashedPassword this is
      # re-applied on every activation, so a password set by hand does not
      # survive the next rebuild. Rescue is an administrator with sudo, an older
      # generation from the boot menu, or installer media with nixos-enter; the
      # initrd emergency shell is closed by NixOS's default emergencyAccess.
      { root.hashedPassword = "!"; }
    ];

    # A fleet host's accounts are exactly what the configuration declares:
    # activation rewrites /etc/passwd and /etc/shadow, so a locally added user
    # or a changed password does not survive the next sync. Passwords then come
    # from the secrets repository (conf.users.accounts.<name>.secrets.password).
    users.mutableUsers = lib.mkDefault (config.conf.fleet.repo.url == null);
  };
}
