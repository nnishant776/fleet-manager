# Per-account secrets, taken from the host's snapshot of the secrets
# repository: users/<name>/<host>.yaml, encrypted to this host. The password
# hash is applied on every activation, which is also how a password is
# rotated. neededForUsers places the decrypted file under /run/secrets-for-users
# before accounts are created. The SSH key is deliberately left under
# /run/secrets rather than linked into ~/.ssh, because sops-nix would create
# the missing parent directories as root. With `secrets.own` the whole file is
# decrypted for the account alone (users/<name>/all), and fleet-user-secret
# prints one of the secrets the user keeps in it.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.secrets;
  host = config.conf.host.name;
  accounts = config.conf.users.accounts;
  withPassword = lib.filterAttrs (_: a: a.secrets.password) accounts;
  withSshKey = lib.filterAttrs (_: a: a.secrets.sshKey) accounts;
  withAuthorizedKeys = lib.filterAttrs (_: a: a.secrets.authorizedKeys) accounts;
  withSudoKeys = lib.filterAttrs (_: a: a.secrets.sudoKeys) accounts;
  withOwn = lib.filterAttrs (_: a: a.secrets.own) accounts;
  userFile = name: "${cfg.statePath}/secrets/users/${name}/${host}.yaml";

  # fleet-user-secret <name> prints secrets.<name> from the whole file of the
  # user who runs it, which `own` decrypts for that user alone. The user is
  # the one running it (id), not what $USER says.
  userSecret = pkgs.writeShellApplication {
    name = "fleet-user-secret";
    runtimeInputs = [ pkgs.coreutils pkgs.yq-go ];
    text = ''
      if [ "$#" -ne 1 ]; then
        echo "usage: fleet-user-secret <name>" >&2
        exit 2
      fi
      name="$1"
      case "$name" in
        *[!A-Za-z0-9_-]* | "")
          echo "fleet-user-secret: a name is letters, digits, - and _" >&2
          exit 2
          ;;
      esac
      file="/run/secrets/users/$(id -un)/all"
      if [ ! -r "$file" ]; then
        echo "fleet-user-secret: $file is not there: this host does not keep your secrets for you (accounts.<you>.secrets.own), or has not synced them yet" >&2
        exit 1
      fi
      if ! value="$(NAME="$name" yq -e '.secrets[strenv(NAME)]' "$file" 2>/dev/null)"; then
        echo "fleet-user-secret: there is no secret named $name in your file on this host (fleet user secret set $name)" >&2
        exit 1
      fi
      printf '%s\n' "$value"
    '';
  };

  # A user's secrets are that user's alone: every secret read from a file
  # under users/<name>/, whoever declares it, belongs to that account or to
  # root, with mode 0400. sops-nix sets whatever is declared, and its default
  # group is the owner's, on NixOS usually `users`, which every account is in.
  usersDir = "${cfg.statePath}/secrets/users/";
  userOf = s:
    let rest = lib.removePrefix usersDir (toString s.sopsFile);
    in if rest == toString s.sopsFile then null else lib.head (lib.splitString "/" rest);
  ownerOnly = lib.mapAttrsToList (n: s: let user = userOf s; in {
    assertion = user == null || (lib.elem s.owner [ null "root" user ] && s.mode == "0400");
    message = "sops.secrets.\"${n}\" comes from ${user}'s secrets file, so it must belong to ${user} or root, with mode 0400; it has owner ${toString s.owner} and mode ${s.mode}.";
  }) config.sops.secrets;
  # The names this module uses, as paths under /run/secrets: a host's own
  # secret with one of them would collide with them there.
  reserved = n: lib.elem n [ "users" "password" ] || lib.hasPrefix "users/" n || lib.hasPrefix "password/" n;
  noClash = lib.mapAttrsToList (n: s: {
    assertion = !(reserved n) || userOf s != null;
    message = "sops.secrets.\"${n}\": names users, password, users/… and password/… are taken by the users' own secrets; give it another name.";
  }) config.sops.secrets;
in {
  config = lib.mkIf (cfg.repo.url != null) {
    assertions = lib.mapAttrsToList (name: a: {
      assertion = a.initialHashedPassword == null;
      message = "conf.users.accounts.${name}: `secrets.password` and `initialHashedPassword` are both set; on a host with conf.secrets.repo.url the password comes from the secrets repository, so remove `initialHashedPassword` or set `secrets.password = false`.";
    }) withPassword ++ ownerOnly ++ noClash;

    # NixOS refuses a configuration in which no administrator can log in, by
    # looking for a password or a key in the configuration. Keys from the
    # secrets arrive at runtime, out of its sight; the sync's check refuses a
    # snapshot that lacks them.
    users.allowNoPasswordLogin = lib.mkIf (lib.any (a: a.privileged) (lib.attrValues withAuthorizedKeys)) (lib.mkDefault true);

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
          mode = "0400";
        };
      }) withSshKey
      # sshd opens the login keys as the account, so the file is the account's,
      # 0400. The sudo keys are root's alone: a key the account could add to
      # would not be a second factor for sudo.
      // lib.mapAttrs' (name: _: lib.nameValuePair "users/${name}/authorized_keys" {
        sopsFile = userFile name;
        key = "authorized_keys";
        owner = name;
        mode = "0400";
      }) withAuthorizedKeys
      // lib.mapAttrs' (name: _: lib.nameValuePair "users/${name}/sudo_keys" {
        sopsFile = userFile name;
        key = "sudo_keys";
        owner = "root";
        mode = "0400";
      }) withSudoKeys
      # The whole file, for the account alone: it holds the user's own secrets
      # (secrets.<name>), which the user stores themselves.
      // lib.mapAttrs' (name: _: lib.nameValuePair "users/${name}/all" {
        sopsFile = userFile name;
        key = "";
        owner = name;
        mode = "0400";
      }) withOwn;

    environment.systemPackages = lib.optional (withOwn != { }) userSecret;

    users.users = lib.mapAttrs (name: _: {
      hashedPasswordFile = config.sops.secrets."password/${name}".path;
    }) withPassword;
  };
}
