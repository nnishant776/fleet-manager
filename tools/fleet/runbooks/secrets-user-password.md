<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-password

Set the user's password on that host.

Hashes the password (yescrypt) and stores the hash in users/\<user>/\<host>.yaml on the host's
branch, host/\<host>, encrypted to that host and your recovery recipient. Hosts apply it at every
activation. For a user the host's file lists (fleet secrets user-enroll) it works on the user's branch,
user/\<user>, instead, and a new file is encrypted to the user too. Push the branch after.

```
fleet secrets user-password [USER] [HOST] [flags]

fleet secrets user-password alice laptop1
printf %s "$pw" | fleet secrets user-password alice laptop1 --password-stdin --yes
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |
| `--host` | The host's name: lowercase letters, digits and '-' |
| `--password-stdin` | read the password from standard input, instead of being asked. Hidden as you type |

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

3. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

4. **Look up where USER's files on HOST are kept.** A user that hosts/HOST.yaml lists keeps their files on a branch of their own, user/USER, and a new file is encrypted to the user as well; any other user's files are on the host's branch. The list is encrypted, so this needs your recovery identity.

5. **Hash the password.** Only the yescrypt hash is stored; the password itself goes nowhere.

   ```
   $ mkpasswd -m yescrypt --stdin   # the password on stdin
   ```

6. **Open a worktree of BRANCH.** The change is made in a temporary worktree of the branch, so that your checkout stays on main and the commit holds this change and nothing else. It goes away at the end, or when the run stops.

7. **Create users/USER/HOST.yaml.** It does not exist yet; it is created empty and encrypted to HOST, the user too when the user is listed, and your recovery recipient.

8. **Store the hash in users/USER/HOST.yaml.** As the key password, which hosts read when accounts.\<user>.secrets.password is on.

9. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push the branch afterwards.

10. **Remove the worktree.** It was only for this change; the commit is on BRANCH in your clone.

   ```
   $ cd REPO && git worktree remove --force WORK
   ```

It needs `git`, `yq`, `sops`, `age`, `mkpasswd` on the PATH; `fleet doctor` says where to get them.
