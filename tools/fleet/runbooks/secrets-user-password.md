<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-password

Set the user's password on that host.

Hashes the password (yescrypt) and stores the hash in users/\<user>/\<host>.yaml,
encrypted to that host and you. Hosts apply it at every activation.

```
fleet secrets user-password [USER] [HOST] [flags]

fleet secrets user-password alice laptop1
printf %s "$pw" | fleet secrets user-password alice laptop1 --password-stdin --yes
```

## Flags

| Flag | |
|---|---|
| `--repo` | The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |
| `--host` | The host's name: lowercase letters, digits and '-' |
| `--password-stdin` | read the password from standard input, instead of being asked. Hidden as you type |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Open the secrets repository.** Each command commits exactly its own change, so the tree must have no uncommitted changes. It builds on the newest commit and encrypts for the recipients that commit names, so that commit must be one a trusted key signed, not whatever was pushed last.

   ```
   $ cd REPO && test -f fleet.yaml
   $ cd REPO && git status --porcelain --untracked-files=no
   $ cd REPO && git verify-commit HEAD
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

4. **Hash the password.** Only the yescrypt hash is stored; the password itself goes nowhere.

   ```
   $ mkpasswd -m yescrypt --stdin   # the password on stdin
   ```

5. **Create users/USER/HOST.yaml.** It does not exist yet; it is created empty and encrypted to HOST and you.

6. **Store the hash in users/USER/HOST.yaml.** As the key password, which hosts read when accounts.\<user>.secrets.password is on.

7. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.

It needs `git`, `yq`, `sops`, `age`, `mkpasswd` on the PATH; `fleet doctor` says where to get them.
