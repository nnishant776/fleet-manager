<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet host secret

Add or change a host secret: encrypt, commit, push, and show how the configuration uses it.

Encrypts one value into hosts/\<host>.yaml of the secrets repository, to that
host and you only, then commits and pushes it. The value is typed hidden,
read from standard input, or, with --from-host, read off the host where it
is kept today, so it never appears on screen. Then it shows the lines that
make the host's configuration use it, and with --sync syncs the host and
checks the secret arrived.

```
fleet host secret [HOST] [NAME] [flags]

fleet host secret neerajpinew wifi/home
fleet host secret neerajpinew hotspot/wls2ccf67579ff5 --from-host /var/lib/hotspot/wls2ccf67579ff5
printf %s "$psk" | fleet host secret pi-gw wifi/home --value-stdin --yes
```

## Flags

| Flag | |
|---|---|
| `--repo` | The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--host` | The host's name: lowercase letters, digits and '-' |
| `--name` | Its key in hosts/\<host>.yaml, and its file under /run/secrets: letters, digits, - and _, with / to group secrets, as hotspot/wls2ccf67579ff5 |
| `--from-host` | A file on the host that holds the value today; empty to type it |
| `--value-stdin` | read the value from standard input, instead of being asked. Hidden as you type; trailing newlines are dropped |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--user` | The account to log in as; support by default, which has sudo without a password |
| `--sync` | Sync the host after pushing and check the secret arrived (its configuration must use it already) |

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

3. **Read the current value off HOST.** As USER, with nothing forwarded; the value is kept in memory and never shown.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n cat FROM-HOST'
   ```

4. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

5. **Encrypt it into hosts/HOST.yaml.** Encrypted to HOST and your recovery key only. An existing value of the same name is replaced; a name that would replace a group of secrets, or go under a value, is refused.

6. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push it afterwards.

7. **Push.** HOST fetches it on its next sync, which checks the commit is signed by an allowed key.

   ```
   $ cd REPO && git push
   ```

8. **Use it in HOST's configuration.** A host only decrypts secrets its configuration declares. Unless it already does, add this to hosts/HOST/default.nix, commit and push:

   > sops.secrets."NAME" = { };   # decrypted to /run/secrets/NAME (root, 0400)
   > # and point the option that needs it at config.sops.secrets."NAME".path;
   > # restartUnits = [ "\<unit>.service" ] restarts a service when the value changes.

9. **Sync HOST.** It fetches the new secrets commit and its configuration, and activation decrypts the secret.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

10. **Check the secret arrived.** sops writes it to /run/secrets when the configuration declares it.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n stat -L -c '\''%U %a'\'' /run/secrets/NAME 2>/dev/null || echo missing'
   ```

It needs `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
