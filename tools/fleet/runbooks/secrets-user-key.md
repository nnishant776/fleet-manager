<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-key

Generate the user's SSH key on that host.

Generates an ed25519 key in a memory-only directory, stores both halves in
users/\<user>/\<host>.yaml on the host's branch, host/\<host>, shreds the plaintext, commits, and shows
the public key. Push the branch after.

```
fleet secrets user-key [USER] [HOST] [flags]

fleet secrets user-key alice laptop1
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |
| `--host` | The host's name: lowercase letters, digits and '-' |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleet setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

2. **Look up host/HOST.** A host's branch is host/\<name>; a host that has none is not enrolled.

3. **Check users/USER/HOST.yaml exists.** The key goes next to the user's password, which user-password creates.

4. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

5. **Open a worktree of host/HOST.** The change is made in a temporary worktree of the host's branch, so that your checkout stays on main and the commit holds this change and nothing else. It goes away at the end, or when the run stops.

6. **Generate the key and store it.** The private key exists in plain text only in a memory-only directory, overwritten and removed however this step ends.

7. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push the branch afterwards.

8. **Remove the worktree.** It was only for this change; the commit is on host/HOST in your clone.

   ```
   $ cd REPO && git worktree remove --force WORK
   ```

9. **Register the public key.** Wherever USER needs it on HOST's behalf, such as their forge account.

   > Public key of USER on HOST; register it on the forge:
   > PUBLIC_KEY

It needs `git`, `yq`, `sops`, `age`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
