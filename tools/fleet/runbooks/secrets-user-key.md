<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-key

Generate the user's SSH key on that host.

Generates an ed25519 key in a memory-only directory, stores both halves in
users/\<user>/\<host>.yaml, shreds the plaintext, commits, and shows the public key.

```
fleet secrets user-key [USER] [HOST] [flags]

fleet secrets user-key alice laptop1
```

## Flags

| Flag | |
|---|---|
| `--repo` | The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |
| `--host` | The host's name: lowercase letters, digits and '-' |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Open the secrets repository.** Each command commits exactly its own change, so the tree must have no uncommitted changes. It builds on the newest commit and encrypts for the recipients that commit names, so that commit must be one a trusted key signed, not whatever was pushed last.

   ```
   $ cd REPO && test -f fleet.yaml
   $ cd REPO && git status --porcelain --untracked-files=no
   $ cd REPO && git verify-commit HEAD
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

2. **Look up HOST in fleet.yaml.** fleet.yaml lists every enrolled host with its age recipient.

   ```
   $ cd REPO && h=HOST yq '.hosts | has(strenv(h))' fleet.yaml
   ```

3. **Check users/USER/HOST.yaml exists.** The key goes next to the user's password, which user-password creates.

4. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

5. **Generate the key and store it.** The private key exists in plain text only in a memory-only directory, overwritten and removed however this step ends.

6. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.

7. **Register the public key.** Wherever USER needs it on HOST's behalf, such as their forge account.

   > Public key of USER on HOST; register it on the forge:
   > PUBLIC_KEY

It needs `git`, `yq`, `sops`, `age`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
