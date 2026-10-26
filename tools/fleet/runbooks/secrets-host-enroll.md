<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets host-enroll

Admit a new host from its enrollment file.

Creates the host's branch, host/\<host>, with its empty encrypted file, encrypted to the host and
your recovery recipient, and the enrollment file as a record; commits, and shows the deploy key to
register. A host that has its branch with this identity already is left as it is. Push the branch after.

```
fleet secrets host-enroll [HOST] [flags]

fleet secrets host-enroll laptop1 --from laptop1.enroll
ssh support@laptop1 sudo cat /var/lib/fleet/laptop1.enroll | fleet secrets host-enroll laptop1 --from -
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--host` | The host's name: lowercase letters, digits and '-' |
| `--from` | The host's enrollment file, as written by fleet-secrets-sync init, or - for standard input |

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

2. **Read the enrollment file.** It holds the host's two public keys: the age recipient its secrets are encrypted to, and the deploy key it fetches them with.

3. **Look up host/HOST.** A host's branch is host/\<name>; a host that has one is enrolled. A branch of another identity is re-keyed, not enrolled over.

4. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

5. **Open a worktree of BRANCH.** The change is made in a temporary worktree of the branch, so that your checkout stays on main and the commit holds this change and nothing else. It goes away at the end, or when the run stops.

6. **Create hosts/HOST.yaml.** sops encrypts an empty document to the host and to your recovery recipient.

7. **Write the enrollment record.** enroll holds the host's public identity as the host wrote it. Hosts do not read it; it is a record of what the branch was made for.

8. **Commit.** Signed with your key, if this checkout signs; hosts accept only signed commits. Push the branch afterwards.

9. **Remove the worktree.** It was only for this change; the commit is on BRANCH in your clone.

   ```
   $ cd REPO && git worktree remove --force WORK
   ```

10. **Register HOST's deploy key.** The host fetches this repository with it; it must be read-only.

   > Secrets deploy key of HOST:
   > DEPLOY_KEY
   > 
   > Register it read-only on the secrets repository (the web page, Settings -> Deploy keys,
   > is the safer route), or with:
   > gh repo deploy-key add \<file holding the key above> --title HOST

It needs `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
