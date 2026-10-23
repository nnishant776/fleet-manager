<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets host-enroll

Admit a new host from its enrollment file.

Records the host's age recipient in fleet.yaml, renders .sops.yaml, creates its
empty encrypted file, commits, and shows the deploy key to register.

```
fleet secrets host-enroll [HOST] [flags]

fleet secrets host-enroll laptop1 --from laptop1.enroll
ssh support@laptop1 sudo cat /var/lib/fleet/laptop1.enroll | fleet secrets host-enroll laptop1 --from -
```

## Flags

| Flag | |
|---|---|
| `--repo` | The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--host` | The host's name: lowercase letters, digits and '-' |
| `--from` | The host's enrollment file, as written by fleet-secrets-sync init, or - for standard input |

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

3. **Check hosts/HOST.yaml does not exist yet.** A host's file is created here, empty, by this command only.

4. **Read the enrollment file.** It holds the host's two public keys: the age recipient its secrets are encrypted to, and the deploy key it fetches them with.

5. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

6. **Record HOST in fleet.yaml.** The registry is the one thing the rules in .sops.yaml are rendered from.

   ```
   $ cd REPO && h=HOST r=RECIPIENT yq -i '.hosts[strenv(h)] = strenv(r)' fleet.yaml
   ```

7. **Render .sops.yaml from fleet.yaml.** Two rules per host, its own file and its users' files, each encrypted to that host and you only.

8. **Re-encrypt HOST's existing files.** Any file already under this host's rules gets its recipients.

9. **Create hosts/HOST.yaml.** sops encrypts an empty document to the recipients the rules name for this path.

10. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.

11. **Register HOST's deploy key.** The host fetches this repository with it; it must be read-only.

   > Secrets deploy key of HOST:
   > DEPLOY_KEY
   > 
   > Register it read-only on the secrets repository (the web page, Settings -> Deploy keys,
   > is the safer route), or with:
   > gh repo deploy-key add \<file holding the key above> --title HOST

It needs `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
