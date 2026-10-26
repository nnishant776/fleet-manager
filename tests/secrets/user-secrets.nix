# Checks, without booting, that a user's secrets stay that user's, on a host
# with secrets (hosts/scratch-secrets, whose alice has an SSH key there):
# - alice's SSH key and its public half belong to alice, mode 0400;
# - a secret read from alice's file but readable by her group, or given to
#   another account, is a build error;
# - a host's own secret named like the users' own (users, users/…,
#   password/…) is a build error, since it would collide with them in
#   /run/secrets;
# - login and sudo keys from a user's file (secrets.authorizedKeys, sudoKeys,
#   support's keysFromSecrets) are the account's (login) or root's (sudo), mode 0400, and make sshd and sudo
#   read them from /run/secrets/users/%u/…, per user; sudoKeys needs a
#   privileged account, and keysFromSecrets a host with secrets.
# Run it with `nix build .#checks.x86_64-linux.secrets-user-owner-only`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "scratch-secrets"; hostDir = ../../hosts/scratch-secrets; };
  # Secrets on, as tests/secrets/vm-test.nix turns them on for this host.
  withSecrets = {
    conf.secrets.repo.url = lib.mkForce "ssh://git@forge/var/lib/forge/secrets.git";
    conf.fleet.repo.url = lib.mkForce "git@forge:config.git";
    conf.fleet.signing.allowedSignersFile = lib.mkForce (builtins.toFile "allowed_signers" "");
    conf.fleet.support.sshKeys = lib.mkForce [ "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDdBK90 support@test" ];
  };
  hostWith = extra: inputs.nixpkgs.lib.nixosSystem
    (hostNode // { modules = hostNode.modules ++ [ withSecrets extra ]; });

  failed = extra:
    map (a: a.message) (lib.filter (a: !a.assertion) (hostWith extra).config.assertions);
  rejects = fragment: extra: lib.any (lib.hasInfix fragment) (failed extra);

  secrets = (hostWith { }).config.sops.secrets;
  aliceFile = secrets."users/alice/ssh_key".sopsFile;
  fromAlice = attrs: { sops.secrets.token = { sopsFile = aliceFile; key = "token"; } // attrs; };
  ownerOnly = "comes from alice's secrets file";
  taken = "are taken by the users' own secrets";

  withKeys = {
    conf.users.accounts.alice.secrets.authorizedKeys = true;
    conf.users.accounts.ops = { privileged = true; secrets.password = false; secrets.sudoKeys = true; };
  };
  keyed = hostWith withKeys;
  keySecrets = keyed.config.sops.secrets;
  sshFiles = keyed.config.services.openssh.authorizedKeysFiles;
  pamFiles = keyed.config.security.pam.sshAgentAuth.authorizedKeysFiles;
  plain = (hostWith { }).config;
  # Support with its keys in the secrets only.
  supportFromSecrets = { conf.fleet.support.sshKeys = lib.mkOverride 40 [ ]; conf.fleet.support.keysFromSecrets = true; };
in
assert lib.assertMsg (failed { } == [ ]) "the host fails its assertions: ${lib.concatStringsSep "; " (failed { })}";
assert lib.assertMsg (secrets."users/alice/ssh_key".owner == "alice" && secrets."users/alice/ssh_key".mode == "0400")
  "alice's SSH key is not alice's alone";
assert lib.assertMsg (secrets."users/alice/ssh_key.pub".mode == "0400") "alice's public key is readable by others";
assert lib.assertMsg (!(rejects ownerOnly (fromAlice { owner = "alice"; mode = "0400"; }))) "a secret of alice's, for alice, is refused";
assert lib.assertMsg (rejects ownerOnly (fromAlice { owner = "alice"; mode = "0440"; })) "a secret of alice's that her group can read is accepted";
assert lib.assertMsg (rejects ownerOnly (fromAlice { owner = "nobody"; })) "a secret of alice's given to another account is accepted";
assert lib.assertMsg (rejects taken { sops.secrets.users.key = "x"; }) "a host secret named users is accepted";
assert lib.assertMsg (rejects taken { sops.secrets."password/root".key = "x"; }) "a host secret named password/… is accepted";
assert lib.assertMsg (keySecrets."users/alice/authorized_keys".owner == "alice" && keySecrets."users/alice/authorized_keys".mode == "0400"
  && keySecrets."users/alice/authorized_keys".key == "authorized_keys")
  "alice's login keys are not alice's alone, mode 0400";
assert lib.assertMsg (keySecrets."users/ops/sudo_keys".owner == "root" && keySecrets."users/ops/sudo_keys".mode == "0400"
  && keySecrets."users/ops/sudo_keys".key == "sudo_keys")
  "ops' sudo keys are not root's, mode 0400";
assert lib.assertMsg (failed withKeys == [ ]) "keys from the secrets fail the host's assertions: ${lib.concatStringsSep "; " (failed withKeys)}";
assert lib.assertMsg (lib.elem "/run/secrets/users/%u/authorized_keys" sshFiles) "sshd does not read the login keys from the secrets";
assert lib.assertMsg (lib.all (lib.hasInfix "%u") (lib.filter (lib.hasPrefix "/run/secrets/") sshFiles)) "a path under /run/secrets in sshd is not per user";
assert lib.assertMsg (!(lib.elem "/run/secrets/users/%u/authorized_keys" plain.services.openssh.authorizedKeysFiles)) "sshd reads login keys from the secrets without an account that has them";
assert lib.assertMsg (lib.elem "/run/secrets/users/%u/sudo_keys" pamFiles && lib.elem "/etc/ssh/sudo_keys.d/%u" pamFiles) "sudo does not read the sudo keys from the secrets";
assert lib.assertMsg (keyed.config.security.pam.services.sudo.sshAgentAuth && lib.hasInfix "noninteractive_auth" keyed.config.security.sudo.extraConfig) "sudo by key is not on for an account with sudoKeys";
assert lib.assertMsg (!plain.security.pam.sshAgentAuth.enable) "sudo by key is on without any sudo keys";
assert lib.assertMsg (rejects "sets secrets.sudoKeys but is not privileged" { conf.users.accounts.alice.secrets.sudoKeys = true; }) "sudoKeys on an unprivileged account is accepted";
assert lib.assertMsg (failed supportFromSecrets == [ ]) "support with its keys in the secrets fails: ${lib.concatStringsSep "; " (failed supportFromSecrets)}";
assert lib.assertMsg ((hostWith supportFromSecrets).config.sops.secrets ? "users/support/authorized_keys") "support's keys are not declared as a secret";
assert lib.assertMsg (rejects "sshKeys is empty and keysFromSecrets is off" { conf.fleet.support.sshKeys = lib.mkOverride 40 [ ]; }) "support without any keys is accepted";
assert lib.assertMsg (rejects "keysFromSecrets is set but conf.secrets.repo.url is null" {
  conf.fleet.support.enable = true;
  conf.fleet.support.keysFromSecrets = true;
  conf.secrets.repo.url = lib.mkOverride 40 null;
}) "keysFromSecrets without a secrets repository is accepted";
pkgs.runCommand "secrets-user-owner-only" { } "touch $out"
