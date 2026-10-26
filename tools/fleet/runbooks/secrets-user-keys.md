<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-keys

Set the SSH keys that may log in as the user, or unlock their sudo, on that host.

Stores the public keys in a file, one per line, as authorized_keys and sudo_keys in
users/\<user>/\<host>.yaml on the host's branch, host/\<host>, encrypted to that host and your
recovery recipient (the file is created if it is not there yet), then commits and pushes the branch.
Each line is checked with ssh-keygen first, so that a typo cannot lock anyone out. Hosts read them
when accounts.\<user>.secrets.authorizedKeys or secrets.sudoKeys is on, at each login, with no
rebuild. A key file replaces the keys that were stored, so list every key that should stay.

```
fleet secrets user-keys [USER] [HOST] [flags]

fleet secrets user-keys alice laptop1 --authorized-keys alice.keys
fleet secrets user-keys admin laptop1 --authorized-keys admin.keys --sudo-keys admin-sudo.keys
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |
| `--host` | The host's name: lowercase letters, digits and '-' |
| `--authorized-keys` | SSH public keys that may log in as the user, one per line; empty to leave them as they are |
| `--sudo-keys` | SSH public keys that unlock the user's sudo, one per line; empty to leave them as they are |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Check the keys.** A line that is not a public key would be stored as it is and lock out whoever relies on it; each non-empty line must be one for ssh-keygen.

2. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleet setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

3. **Look up host/HOST.** A host's branch is host/\<name>; a host that has none is not enrolled.

4. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

5. **Open a worktree of host/HOST.** The change is made in a temporary worktree of the host's branch, so that your checkout stays on main and the commit holds this change and nothing else. It goes away at the end, or when the run stops.

6. **Create users/USER/HOST.yaml.** It does not exist yet; it is created empty and encrypted to HOST and your recovery recipient.

7. **Store the login keys in users/USER/HOST.yaml.** As the key authorized_keys, which hosts read when accounts.\<user>.secrets.authorizedKeys is on.

   ```
   $ cd WORK && sops set --value-stdin users/USER/HOST.yaml '["authorized_keys"]'   # the login keys on stdin
   ```

8. **Store the sudo keys in users/USER/HOST.yaml.** As the key sudo_keys, which hosts read when accounts.\<user>.secrets.sudoKeys is on.

   ```
   $ cd WORK && sops set --value-stdin users/USER/HOST.yaml '["sudo_keys"]'   # the sudo keys on stdin
   ```

9. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push the branch afterwards.

10. **Remove the worktree.** It was only for this change; the commit is on host/HOST in your clone.

   ```
   $ cd REPO && git worktree remove --force WORK
   ```

11. **Push host/HOST.** HOST fetches its branch from the forge; it accepts the commit only if it is signed by an allowed key.

   ```
   $ cd REPO && git push origin host/HOST
   ```

It needs `git`, `yq`, `sops`, `age`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
