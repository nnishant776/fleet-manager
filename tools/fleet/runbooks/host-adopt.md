<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet host adopt

Store the password and keys of the owner a personal machine made at its first boot, and print the declaration to add.

A personal machine (conf.fleet.personal) lets its owner make their own account at the first boot, and
keeps that account out of the configuration. This starts bringing it in. fleet logs in to the host as
support and reads who the owner is (/var/lib/fleet/owner), their full name and home, their password hash
(never shown, and passed on standard input only) and the login keys in their ~/.ssh/authorized_keys.
It stores the hash and the keys in the secrets repository (fleet secrets user-password --hash-stdin and
user-keys), after fleet secrets user-enroll when you give the owner's enrollment file with --from, which
is for an owner who will change their own password and keys later (fleet user init makes the file).
It changes nothing in the inventory. It prints the declaration for you to put into the host's own
default.nix, and the commands that finish: publish with a sync, and check the host. The owner keeps
their uid, files and password: NixOS keeps an existing account's ids when it becomes declared.

```
fleet host adopt [HOST] [flags]

fleet host adopt laptop1 --flake inventory --repo secrets
fleet host adopt laptop1 --from ann.enroll --flake inventory --repo secrets
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--user` | The account to log in as; support by default, which has sudo without a password, so nothing needs forwarding |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--from` | The owner's enrollment file (fleet user init), when they will manage their own password and keys later; none: they ask you |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Read how HOST is configured.** Only a personal machine has an owner to adopt. Whether the owner is an administrator is read from its configuration, and carried over. Nothing is written.

4. **Reach HOST.** As USER, with no agent forwarded, and only to a host whose key this machine already knows.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'id -un; hostname'
   ```

5. **Read the owner from HOST.** As support, with sudo: who made the account (/var/lib/fleet/owner), what getent says of it, the password hash from the shadow file and the login keys in the owner's home. The hash is kept in memory only, never shown, and passed on standard input.

6. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleet setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

7. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

8. **Enroll the owner in the secrets repository.** With their enrollment file the owner gets a branch of their own, user/\<owner>, and keeps their password and keys there with fleet user. This is fleet secrets user-enroll, run for the owner and this host.

9. **Store the owner's password hash.** As it is on the host: the owner keeps the password they have. This is fleet secrets user-password --hash-stdin.

10. **Store the owner's login keys.** The keys of their ~/.ssh/authorized_keys, which the configuration will read from the secrets instead. This is fleet secrets user-keys --authorized-keys.

11. **Push the secrets.** HOST fetches them from the forge on its next sync, and accepts the commits only if a key it trusts signed them. What the commands above left unpushed goes now.

12. **Next.** The declaration to add to the inventory, and the commands that finish.

It needs `git`, `nix`, `tar`, `ssh`, `yq`, `sops`, `age`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
