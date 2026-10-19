<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets host-rekey

Point a known host at a new identity.

For a host reinstalled with a new identity: records its new recipient, renders
.sops.yaml, re-encrypts its files to it, commits, and shows the new deploy key.

```
fleet secrets host-rekey [HOST] [flags]

fleet secrets host-rekey laptop1 --from laptop1.enroll
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

1. **Open the secrets repository.** Each command commits exactly its own change, so the tree must have no uncommitted changes.

   ```
   $ cd REPO && test -f fleet.yaml
   $ cd REPO && git status --porcelain --untracked-files=no
   ```

2. **Look up HOST in fleet.yaml.** fleet.yaml lists every enrolled host with its age recipient.

   ```
   $ cd REPO && h=HOST yq '.hosts | has(strenv(h))' fleet.yaml
   ```

3. **Read the enrollment file.** It holds the host's two public keys: the age recipient its secrets are encrypted to, and the deploy key it fetches them with.

4. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

5. **Record HOST in fleet.yaml.** The registry is the one thing the rules in .sops.yaml are rendered from.

   ```
   $ cd REPO && h=HOST r=RECIPIENT yq -i '.hosts[strenv(h)] = strenv(r)' fleet.yaml
   ```

6. **Render .sops.yaml from fleet.yaml.** Two rules per host, its own file and its users' files, each encrypted to that host and you only.

7. **Re-encrypt HOST's files to its new identity.** Its own file and its users' files.

8. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.

9. **Register HOST's deploy key.** The host fetches this repository with it; it must be read-only.

   > Secrets deploy key of HOST:
   > DEPLOY_KEY
   > 
   > Register it read-only on the secrets repository (the web page, Settings -> Deploy keys,
   > is the safer route), or with:
   > gh repo deploy-key add \<file holding the key above> --title HOST
   > 
   > Then delete the host's previous deploy key on the forge.

It needs `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
