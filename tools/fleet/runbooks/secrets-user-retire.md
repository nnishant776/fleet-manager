<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-retire

Take a user off the hosts that list them and delete their branch.

Removes users.\<user> from the file of every host that lists the user (a commit on the host's branch,
user-retire: \<user> on \<host>, pushed), deletes user/\<user> in your clone after saying what it held,
and shows what is still to do: deleting it on the forge. Take the user's accounts out of the
inventory first.

```
fleet secrets user-retire [USER] [flags]

fleet secrets user-retire alice
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

2. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

3. **Find the hosts that list USER.** The hosts whose file has users.\<user>. The keys of an encrypted file are in clear, so the list is read as it is in git.

4. **Remove USER from their files.** In a temporary worktree of each host's branch: users.\<user> removed from hosts/\<host>.yaml, which sops does with your recovery identity. One commit per host. From the next sync on the host takes the user's files from its own branch again, if it has any.

5. **Push the hosts' branches.** A host stops reading the user's branch on its next sync, which checks the commit is signed by an allowed key.

6. **Show what user/USER held.** The files that go with the branch.

   ```
   $ cd REPO && git ls-tree -r --name-only USER_REF
   ```

7. **Delete the local branch user/USER.** With it go the user's files on it.

   ```
   $ cd REPO && git branch -D -q user/USER
   ```

8. **Still to do.** The branch is gone from your clone only.

   > USER is retired. Still to do:
   > 1. Delete the branch on the forge, which takes the user's files away from the hosts:
   > git push origin --delete user/USER
   > 2. Rotate what the user could decrypt: their password and SSH keys on the hosts they were listed on,
   > and revoke their public keys on the forge now.

It needs `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
