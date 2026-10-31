# 8. Secrets

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 7. Signing commits and trusting them](signing-and-trust.md) · [Contents](README.md) · [9. A private fleet →](private-fleet.md)
<!-- /book:top -->

How passwords, keys and other secrets reach a host without ever entering
fleet-manager: the secrets repository and its files, who can decrypt what, what
a host does with its secrets, and the rules for working in it. It builds on
[Signing commits and trusting them](signing-and-trust.md). What to do, step by
step, is in [Secrets runbooks](secrets-runbooks.md); the options are under
[`conf.secrets`](configuration.md#confsecrets) in *Configuration reference*.

Secrets never enter fleet-manager, which is public: the ciphertext lives in a
separate **private** repository, with one read-only deploy key per host, and
branch protection where the forge offers it (GitHub does not, for a private
repository on its free plan). It holds sops ciphertext and nothing else. Each
host has a branch of its own, `host/<name>`, fetches only that one with its
deploy key, verifies the commit signature, and decrypts the files at
activation with sops-nix. A user can have a branch of their own too, which
the hosts that list them read as well.

<!-- book:sections -->
In this chapter:

- [The secrets repository](#the-secrets-repository)
- [Users with a branch of their own](#users-with-a-branch-of-their-own)
- [Trust model](#trust-model)
- [On the host](#on-the-host)
- [Host secrets](#host-secrets)
- [Rules](#rules)
- [The tools](#the-tools)
<!-- /book:sections -->

## The secrets repository

One branch per host, and one per user who changes their own files. `main` holds
only a README and the repository's tooling; no host reads it.

```
main
  README.md
  .gitignore
  .githooks/pre-commit        the commit check that fleet setup installs a reviewed copy of

host/laptop1                  the only branch laptop1 fetches
  hosts/laptop1.yaml          secrets of laptop1, readable by laptop1 + maintainer
  users/alice/laptop1.yaml    alice's secrets on laptop1, readable by laptop1 + maintainer
  enroll                      laptop1's public enrollment record

host/server1
  hosts/server1.yaml
  users/alice/server1.yaml    alice's secrets on server1: a different SSH key, if any
  enroll

user/bob                      bob's own branch; a host reads it only if its hosts/<h>.yaml lists bob
  users/bob/laptop1.yaml      bob's secrets on laptop1, readable by bob + laptop1 + maintainer
  users/bob/server1.yaml      the same for each host bob has an account on
  enroll                      bob's public enrollment record
```

Every file is encrypted to exactly one host plus the maintainer's recovery
recipient, and the file says so in its sops metadata. A file on a user's branch
is encrypted to the user as well. A user with accounts on three hosts has three
files, with three different SSH keys. Losing one host exposes only that host's
files, and the host never has another host's branch on disk, only the branches
of the users its own file lists.

| File | Key | Read when | Written by |
|---|---|---|---|
| `users/<u>/<h>.yaml` | `password` | `accounts.<u>.secrets.password` (default `true`) | `fleet secrets user-password` |
| | `ssh_key`, `ssh_key_pub` | `accounts.<u>.secrets.sshKey = true` | `fleet secrets user-key` |
| | `authorized_keys` | `accounts.<u>.secrets.authorizedKeys = true`; for `support`, `conf.fleet.support.keysFromSecrets = true` | `fleet secrets user-keys`, or `fleet user keys` on a user's own branch |
| | `sudo_keys` | `accounts.<u>.secrets.sudoKeys = true` (privileged accounts only) | the same |
| | `secrets.<name>` | `accounts.<u>.secrets.own = true`, read with `fleet-user-secret <name>` | `fleet user secret` |
| `hosts/<h>.yaml` | `support_password` | `conf.fleet.support.consolePassword = true` | `fleet host secret`, or `sops` by hand |
| | any `<name>` | a module declares `sops.secrets.<name>` | `fleet host secret`, or `sops` by hand |
| | `users` | reserved: the users whose branch the host reads (each with `signing_key` and `age_recipient`) | `fleet secrets user-enroll`, `user-retire` |
| `enroll` | | not read by the host; a record of the identity the branch was made for | `fleet secrets host-enroll`; on `user/<u>`, `fleet secrets user-enroll` |

`authorized_keys` and `sudo_keys` hold public keys, one per line. On a host
with secrets they are the only place its login and sudo keys can be: keys in the
configuration are a build error there, and sshd does not read
`~/.ssh/authorized_keys`. A change applies at the host's next sync, with no
rebuild.

There is no registry of hosts and no `.sops.yaml`. Each file lists who can
decrypt it in its own sops metadata: the host's age recipient and the
maintainer's recovery recipient, and nobody else. A new file for a host is
encrypted to the same two recipients as `hosts/<host>.yaml`, so a file is
readable by exactly one host plus the maintainer's recovery key. The recovery
recipient is read from `recovery.pub` in the `fleet` folder of
`$XDG_STATE_HOME` (`~/.local/state` when it is unset), which `fleet setup`
writes once, asking for the recovery identity's passphrase. A host file holds
whatever that machine needs; `support_password` is a reserved name. No key of
the host's own is stored there: the host makes its keys itself, and they never
leave it.

A host opts in with `conf.secrets.repo.url`:

```nix
# hosts/laptop1/default.nix
conf.hardware.disk = { enable = true; device = "/dev/nvme0n1"; encrypt = true; volumes.swap.size = "32G"; };
conf.fleet.repo.url = "https://github.com/org/fleet-config.git";
conf.fleet.signing.allowedSignersFile = ../../fleet/allowed_signers;
conf.secrets.repo.url = "git@github.com:example-org/secrets.git";
conf.fleet.support.keysFromSecrets = true;   # support's keys are in its file on the host's branch
conf.users.accounts.alice = { fullName = "Alice"; };   # password from secrets; no SSH key yet
```

The secrets repository is checked against the same signers file, so its
commits, including the ones `fleet-secrets` makes, must be signed too. Set up
commit signing in that checkout as well (see
[Signing commits and trusting them](signing-and-trust.md)).

## Users with a branch of their own

A user can have a branch of their own, `user/<name>`, holding their files for
every host they have an account on. A host reads it only when its own file,
`hosts/<host>.yaml`, lists the user under the reserved key `users`, with the
user's SSH signing key and age recipient; the list is encrypted like any other
value, and only the maintainer changes it. A commit on a user's branch is
accepted when it is signed by that user's key or by a key in the signers
file. A user who is not listed has their files on the host's branch, as alice
does here. A user with a branch of their own changes their files themselves,
without the maintainer: they run `fleet user init` once, which makes their age
identity, an SSH signing key and the enrollment file to give the maintainer,
and after that `fleet user password`, `fleet user keys` and `fleet user secret`
change their files on their own branch, sign the commit with their own key and
push it.

## Trust model

| Key | Lives | Can |
|---|---|---|
| Maintainer **recovery identity** (age) | `~/.fleet/recovery.age`, passphrase-encrypted, plus an offline copy | decrypt and re-encrypt every file |
| Maintainer **recovery recipient** (its public half) | `recovery.pub` in the `fleet` folder of `${XDG_STATE_HOME:-~/.local/state}`, written by `fleet setup` | nothing but be encrypted to; lets the tools encrypt without the passphrase |
| Maintainer **signing key** (SSH) | the maintainer's machine, ideally a hardware key | sign commits; hosts accept only commits signed by a key in the `allowed_signers` their configuration comes with |
| User **age identity** | `~/.fleet/user.age`, passphrase-encrypted, on the user's machine; made by `fleet user init` | decrypt that user's files, on their branch, for every host they have an account on |
| User **signing key** (SSH) | `~/.ssh/fleet_user_signing_key`, on the user's machine | sign commits to the user's own branch; a host accepts them because its file lists the key |
| Host **age key** | `/var/lib/fleet/age.key`, on the host only | decrypt that host's files |
| Host **secrets deploy key** (SSH) | `/var/lib/fleet/secrets_deploy_key`, on the host only | *read* the secrets repository (registered read-only) |
| Host **configuration deploy key** (SSH) | `/var/lib/fleet/config_deploy_key`, on the host only | *read* a private configuration repository (registered read-only there); unused for a public one |

Host private keys are generated on the host and never leave it, except through
an explicit `fleet-identity --export` for a reinstall. The maintainer only sees
their public halves, in the host's **enrollment file**, which is also kept on
its branch as `enroll`:

```yaml
version: 1
host: laptop1
created: 2026-10-11T09:00:00Z
age_recipient: age1…
secrets_deploy_pubkey: ssh-ed25519 AAAA… fleet-secrets@laptop1
config_deploy_pubkey: ssh-ed25519 AAAA… fleet-config@laptop1
```

The enrollment file is not secret, but its **integrity** matters: whoever
substitutes their own `age_recipient` receives that host's secrets. Take it
from the machine through a channel you trust: your own eyes on the QR code, a
USB stick you hold, or SSH to the host as `support` with your own key.

A user's enrollment file (`version`, `user`, `age_recipient`, `signing_key`) is
public too, and its integrity matters in the same way: whoever substitutes their
own `age_recipient` or `signing_key` reads or writes that user's files. Take it
from the user through a channel you trust. A host reads a user's branch only
because its own file, which only the maintainer changes, lists the user, so a
user can touch their own files and no one else's. A commit on `user/<u>` counts
when it is signed by the user's key or by a key in `allowed_signers`.

One limit: a deploy key cannot be limited to a branch on GitHub, so root on a
host could use its key to fetch the other branches. It still cannot decrypt
them, because their files are encrypted to other hosts.

## On the host

Each host has an identity of three keys, all root-only files in
`conf.secrets.statePath`, which is its own volume (`fleet`), so a rebuild or a
manual reinstall that keeps the volume leaves it alone: an age key (`age.key`) that decrypts, an SSH secrets
deploy key (`secrets_deploy_key`) that only fetches ciphertext, and an SSH
configuration deploy key (`config_deploy_key`) that fetches a private
configuration, from an SSH `conf.fleet.repo.url`. Their public halves are
`secrets_deploy_pubkey` and `config_deploy_pubkey` in its enrollment file. `os-install` erases the
whole disk, so it generates new ones, or restores them with `--identity`. No
copy exists anywhere else, so a reinstall either carries the identity over by hand or re-keys the host. Each
sync fetches the host's branch of the secrets repository into a temporary repository, verifies the
commit signature, checks that the commit follows the one it accepted last
(kept in `<statePath>/accepted/host`), and reads only this host's files, with
no checkout. For each user its file lists, it fetches the user's branch and
checks that its newest commit is signed by the user or the maintainer, follows
the one accepted last (`accepted/user-<name>`), and holds a file for this host
encrypted to exactly the user, this host and the recovery recipient, with a
`password`, if any, that is a password hash. A user whose branch fails a check
keeps the file the host had, and the others carry on; the problem is listed at
every login, in `fleet-sync-status` and in `fleet host check`, until a good
commit fixes it. The files go into a snapshot in `<statePath>/secrets`, with a
`manifest` saying which commit each file came from, named after the manifest,
so a sync that brings nothing new changes nothing; sops-nix decrypts the
snapshot at activation, and a snapshot that changed while the configuration
did not is activated again. Before building, the sync also refuses a new
configuration that needs a secret the snapshot lacks.

| Path | Owner / mode | Used by |
|---|---|---|
| `/run/secrets-for-users/password/alice` | root, 0400 | Account creation (`hashedPasswordFile`), available before accounts exist |
| `/run/secrets/users/alice/ssh_key` | alice, 0400 | Her SSH client and git signing; only if `secrets.sshKey` is on |
| `/run/secrets/users/alice/ssh_key.pub` | alice, 0400 | For reference |
| `/run/secrets/users/alice/authorized_keys` | alice, 0400 | sshd, as an `AuthorizedKeysFile` with `%u`, so a file admits only its own account; only if `secrets.authorizedKeys` is on (or for support, `fleet.support.keysFromSecrets`). sshd opens it as the account, which is why the file is hers |
| `/run/secrets/users/admin/sudo_keys` | root, 0400 | sudo's SSH-agent check, for that account only; only if `secrets.sudoKeys` is on. Root's, so the account cannot add a key |
| `/run/secrets/users/alice/all` | alice, 0400 | The whole of alice's file, so that she can read what she keeps in it herself, with `fleet-user-secret <name>`; only if `secrets.own` is on |
| `/run/secrets/<host secret>` | per module | Whichever module declared it |

A user's secrets are that user's alone. Every secret read from a file under
`users/<name>/`, whichever module or host declares it, must belong to that
account or to root, with mode 0400, or the host does not build. sops-nix sets
whatever is declared, and its default group is the owner's, which on NixOS is
usually `users`, so 0440 would let every account read it. The names
`users`, `password`, `users/…` and `password/…` are the users' own: a host
secret named so does not build either.

`/run/secrets` is kept in memory, in a ramfs that is never swapped, and rebuilt at every activation and boot, and the key is
deliberately not placed in `~/.ssh`: nothing is copied to disk, and the Home
Manager baseline points SSH and git at the `/run/secrets` path. Anyone with
root on a machine can read what is encrypted to it, which is no more than root
can read from `/etc/shadow` already.

## Host secrets

`hosts/<host>.yaml` starts empty (`{}`) and holds whatever the machine needs.
A module reads key `<name>` from this file by declaring
`sops.secrets."<name>" = { };`. The default file is the host's own, and the
decrypted value appears at `/run/secrets/<name>`.

Secrets can be grouped. A `/` in the name nests it, both in the file and
under `/run/secrets`:

```yaml
hotspot:
  wlu001a2b3c4d5e: …   # sops.secrets."hotspot/wlu001a2b3c4d5e", at /run/secrets/hotspot/wlu001a2b3c4d5e
wifi:
  home: …              # sops.secrets."wifi/home", at /run/secrets/wifi/home
```

With sops directly, the path is `'["hotspot"]["wlu001a2b3c4d5e"]'`. sops
encrypts the values only, so the names of keys and groups stay readable in
the file. A group can't also hold a value of its own.

Names the fleet itself uses, at the top level:

- `support_password`: a yescrypt hash for console login as `support`, read
  when `conf.fleet.support.consolePassword = true`.
- `users`: reserved, the users whose branch the host reads (see the table
  above). Do not set it by hand.

A host secret of your own cannot be named `users` or `password`, nor start with
`users/` or `password/`: those names are taken by the users' secrets.

A secret that every host needs still goes into each host's file separately.
There is deliberately no shared file, because one compromised host would then
expose it for everyone.

How to set one is under
[Host secrets](secrets-runbooks.md#host-secrets) in *Secrets runbooks*.

## Rules

1. **Never commit plaintext.** Files under `hosts/` and `users/` are created by
   `fleet` or edited with `sops`, which re-encrypts on save. The pre-commit
   hook enforces this. Don't bypass it with `--no-verify`.
2. **Sign every commit** on a host branch with a key in the configuration's
   `allowed_signers`. A host verifies the commit at the tip of its branch and
   refuses anything else (sync exit code 11), so one unsigned commit on top
   stops secret updates for that host. On a user's branch a commit is signed
   by the user or by the maintainer; if it is not, the host keeps the user's
   previous file and names the user in its notices. The tools refuse to build on a branch
   whose tip is not signed by a trusted key, or that does not follow the commit
   you trusted last on it (an old commit served again). Do not merge on the forge:
   GitHub's merge buttons drop your signature or replace it with GitHub's own,
   which no host trusts.
3. **Don't touch a host branch with a plain checkout.** The commands work in a
   temporary worktree of the branch and leave the clone on `main`. For a
   manual edit, use `git worktree add` yourself and remove it afterwards.
4. **One change per commit.** The commands commit their own change.
5. **Push the branch after every change.** `fleet host enroll`,
   `fleet host secret`, `fleet secrets user-keys`, `user-enroll` and
   `user-retire` push for you. The other `fleet secrets` commands do not: they
   say so, and you run `git push origin host/<h>` (or `user/<u>`). Hosts pick it
   up on their next sync; nothing is pushed to them.
6. **Keys live in the secrets repository, not in the configuration.** On a host
   with secrets, login and sudo keys go in the account's file. Listing keys in
   the configuration is a build error on that host, by design: one place for
   them.

## The tools

Two commands carry the work. `fleet-secrets` (`nix run .#fleet-secrets`) is the
maintainer's tool, run in a clone of the secrets repository, with `--recovery
~/.fleet/recovery.age` for the passphrase-protected recovery identity: `host-enroll`,
`host-rekey`, `host-retire`, `user-password` and `user-key`. The clone stays on
`main`: a command for a host works in a temporary worktree of that host's
branch, commits there, and removes the worktree, and it refuses to build on a
branch whose newest commit no trusted key signed. `host-enroll` makes the
branch, `host-rekey` replaces the host's recipient in every file on it, and
`host-retire` deletes it. None of them pushes; push the branch afterwards with
`git push origin host/laptop1` (`fleet host enroll`, `fleet host secret`, `fleet secrets user-keys`, `user-enroll` and
`user-retire` push for themselves). For a user the host's file lists,
`user-password` and `user-key` (and `fleet secrets user-keys`) work on the user's
branch instead, and `host-rekey` replaces the host's recipient in the user's
files there too. Two more, in the
Go tool only: `fleet secrets user-enroll <name> --from <name>.enroll` gives a
user a branch of their own, and `fleet secrets user-retire <name>` takes the
user off every host's list. A user with a branch of their own uses `fleet user`
instead, in their own clone of the secrets repository, with their own identity
(`~/.fleet/user.age`, asked for its passphrase) and signing key: `fleet user
init`, `fleet user password [HOST…|--all-hosts]`, `fleet user keys` (the same
flags as `fleet secrets user-keys`), and `fleet user secret set|unset NAME`.
Each fetches the branch, refuses to build on a newest commit that neither their
own key nor a key in their copy of the configuration repository's signers
(`fleet trust signers`, or `--signers FILE`) signed, makes one signed commit with
their key (their git configuration is not touched) and pushes the branch; a
change that changes nothing makes no commit. The commands that encrypt need your recovery
recipient in the cache `fleet setup` fills, and stop with "run fleet setup"
without it. `fleet setup` also installs, in the secrets
clone, a copy of the repository's pre-commit hook, which refuses a file under
`hosts/` or `users/` that is not ciphertext. `fleet-secrets-sync` (`init`, `sync` and `check`, which the sync and the
installer run) and `fleet-identity` are installed on opted-in hosts;
`fleet-identity` prints the host's enrollment file (`--qr` for a QR code); it needs `sudo`, since the state
directory is root's; `sudo fleet-identity --export`
writes the private identity document for a planned reinstall. For a local
test without a hosting service, `nix run .#secrets-harness` stands in for the
secrets repository and the configuration upstream; see
[`tests/secrets/README.md`](../tests/secrets/README.md).

`fleet secrets …`, the Go port, does the same as `fleet-secrets`, the shell
tool, for `host-enroll`, `host-rekey`, `host-retire`, `user-password` and
`user-key`. `user-keys`, `user-enroll` and `user-retire` exist only in the Go
port. The runbooks use `fleet secrets`.

<!-- book:bottom -->
---

[← 7. Signing commits and trusting them](signing-and-trust.md) · [Contents](README.md) · [9. A private fleet →](private-fleet.md)
<!-- /book:bottom -->
