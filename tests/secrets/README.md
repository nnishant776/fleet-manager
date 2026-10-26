# Secrets test harness

A local stand-in for the private secrets repository and the configuration
upstream, so the secrets design can be tested without a hosting service.

    nix run .#secrets-harness -- up [DIR]      # default DIR: /tmp/secrets-harness
    nix run .#secrets-harness -- status [DIR]
    nix run .#secrets-harness -- down [DIR]

Run it from inside the flake checkout. `up` is idempotent: it creates only what
is missing. `down` stops the sshd and keeps the files.

`up` creates in DIR:

- `recovery.key` (maintainer age key), `host.key` (the scratch host's age
  identity), `secrets_deploy_key` (its secrets deploy key), `signing_ed25519` with
  `allowed_signers` (principal `maintainer@test`), `support_ed25519`.
- `sshd/`: host key, `sshd_config` and `authorized_keys` (the deploy key). An
  sshd runs as the current user on port 2222 and accepts only that user.
- `secrets.git` (bare) and `secrets/` (a clone): `main` with a README, and the
  scratch host's branch, `host/scratch-secrets`, with
  `hosts/scratch-secrets.yaml` (`support_password`, hash of `support`) and
  `users/alice/scratch-secrets.yaml` (`password`, hash of `alice`, plus
  `ssh_key` and `ssh_key_pub`) and `users/support/scratch-secrets.yaml`
  (`authorized_keys`, the support key), encrypted to the recovery key and `host.key`,
  in one signed commit. There is no `fleet.yaml` and no `.sops.yaml`: the host
  tracks its branch, which is the default of `conf.secrets.repo.ref`.
- `config.git` (bare): this checkout's files plus `hosts/scratch-secrets/*`, in
  one signed commit. Delete it and run `up` again to refresh the snapshot.

It also writes `hosts/scratch-secrets/harness.nix` and `allowed_signers`
(machine-specific; `git add -N`-ed, do not commit them).

A QEMU guest with user networking reaches the host's sshd at `10.0.2.2:2222`;
the URLs in `harness.nix` use that address. The scratch host's deploy key is
`DIR/secrets_deploy_key` and its age identity `DIR/host.key`.

## End-to-end VM test

`nix build .#checks.x86_64-linux.secrets-sync` runs a NixOS VM test
(`vm-test.nix`): a `forge` node serves signed bare repositories over sshd and a
`fleet` node built from the scratch host has to bootstrap, snapshot, activate
and refuse an unsigned commit on its branch. Keys come from the secrets too:
alice logs in with a key that is in no configuration (her file has
`authorized_keys`, and the forge holds the private half), support does the same
with `fleet.support.keysFromSecrets` and its key in `users/support/<host>.yaml`,
and `ops`, a privileged account, unlocks sudo with a key from `sudo_keys` through a
forwarded agent, as `tests/users/ssh-key-sudo.nix` does for `sudoSshKeys`. A check
with a file that lacks `authorized_keys` is refused, and `fleet secrets user-keys`
is run on the forge: a line that is not a key stops it, and keys it stores reach
the host's snapshot. The forge also plays the
maintainer: `fleet host secret` works in a worktree of `host/scratch-secrets` in
a clone that stays on `main`, with the recovery recipient cached the way
`fleet setup` leaves it. It needs KVM and takes a few minutes; the CI
workflow does not run it.
