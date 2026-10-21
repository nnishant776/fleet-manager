# Checks, without booting, that a user's secrets stay that user's, on a host
# with secrets (hosts/scratch-secrets, whose alice has an SSH key there):
# - alice's SSH key and its public half belong to alice, mode 0400;
# - a secret read from alice's file but readable by her group, or given to
#   another account, is a build error;
# - a host's own secret named like the users' own (users, users/…,
#   password/…) is a build error, since it would collide with them in
#   /run/secrets.
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
pkgs.runCommand "secrets-user-owner-only" { } "touch $out"
