<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets host-retire

Remove a host and its files.

Removes the host from fleet.yaml and .sops.yaml, deletes its files, re-encrypts
the rest as a safety net, commits, and lists what is still to do.

```
fleet secrets host-retire [HOST] [flags]

fleet secrets host-retire laptop1
```

## Flags

| Flag | |
|---|---|
| `--repo` | The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
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

3. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

4. **Remove HOST from fleet.yaml.** Its recipient goes, and with it the two rules .sops.yaml has for it.

   ```
   $ cd REPO && h=HOST yq -i 'del(.hosts[strenv(h)])' fleet.yaml
   ```

5. **Render .sops.yaml from fleet.yaml.** Two rules per host, its own file and its users' files, each encrypted to that host and you only.

6. **Delete HOST's files.** Its own file and its users' files on it.

7. **Re-encrypt every other file.** A safety net: no other file ever included this host.

8. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.

9. **Still to do.** The commit takes effect when pushed; the host's access ends when its deploy key goes.

   > HOST is retired. Still to do:
   > 1. Push this commit.
   > 2. Delete the host's deploy key on the forge; that takes effect at once.
   > 3. Rotate what the host could decrypt, which is only its own files:
   > its users' passwords and SSH keys on HOST (revoke their public keys
   > on the forge now) and whatever was in hosts/HOST.yaml.

It needs `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
