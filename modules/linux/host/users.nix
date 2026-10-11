{ config, lib, ... }:
{
  config = {
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
