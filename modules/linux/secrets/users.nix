# Per-account secrets, taken from the host's snapshot of the secrets
# repository: users/<name>/<host>.yaml, encrypted to this host. The password
# hash is applied on every activation, which is also how a password is
# rotated. neededForUsers places the decrypted file under /run/secrets-for-users
# before accounts are created. The SSH key is deliberately left under
# /run/secrets rather than linked into ~/.ssh, because sops-nix would create
# the missing parent directories as root.
{ config, lib, ... }:
let
  cfg = config.conf.secrets;
  host = config.conf.host.name;
  accounts = config.conf.users.accounts;
  withPassword = lib.filterAttrs (_: a: a.secrets.password) accounts;
  withSshKey = lib.filterAttrs (_: a: a.secrets.sshKey) accounts;
  userFile = name: "${cfg.statePath}/secrets/users/${name}/${host}.yaml";
in {
  config = lib.mkIf (cfg.repo.url != null) {
    assertions = lib.mapAttrsToList (name: a: {
      assertion = a.initialHashedPassword == null;
      message = "conf.users.accounts.${name}: `secrets.password` and `initialHashedPassword` are both set; on a host with conf.secrets.repo.url the password comes from the secrets repository, so remove `initialHashedPassword` or set `secrets.password = false`.";
    }) withPassword;

    sops.secrets =
      lib.mapAttrs' (name: _: lib.nameValuePair "password/${name}" {
        sopsFile = userFile name;
        key = "password";
        neededForUsers = true;
      }) withPassword
      // lib.concatMapAttrs (name: _: {
        "users/${name}/ssh_key" = {
          sopsFile = userFile name;
          key = "ssh_key";
          owner = name;
          mode = "0400";
        };
        "users/${name}/ssh_key.pub" = {
          sopsFile = userFile name;
          key = "ssh_key_pub";
          owner = name;
          mode = "0444";
        };
      }) withSshKey;

    users.users = lib.mapAttrs (name: _: {
      hashedPasswordFile = config.sops.secrets."password/${name}".path;
    }) withPassword;
  };
}
