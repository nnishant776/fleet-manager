<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet setup

Set up this machine as the maintainer's: recovery identity, signing key, both checkouts.

The one-time setup: a passphrase-encrypted recovery identity, which can decrypt
every secret; an SSH key that signs commits, trusted by the fleet through
fleet/allowed_signers; both checkouts set to sign every commit; and the recovery
recipient, cached so that the secrets commands can encrypt to it without asking
for the passphrase. Each step that is done already is skipped, so it is safe to
run again.

```
fleet setup [flags]

fleet setup --config ~/Projects/nixos-config --repo ~/Projects/secrets
```

## Flags

| Flag | |
|---|---|
| `--config` | The fleet-manager checkout; defaults to this directory's flake, then /etc/nixos |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Where the passphrase-encrypted age identity lives; ~/.fleet/recovery.age by default |
| `--signing-key` | The SSH private key that signs commits; created if it does not exist |
| `--label` | Free text that git does not compare with anything; your GitHub noreply address, for example |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Create your recovery identity.** It can decrypt every secret, so it is written straight into a passphrase-encrypted file and never touches the disk in clear. age asks for the passphrase twice. Keep a copy offline.

   ```
   $ sh -c 'umask 077 && mkdir -p "$(dirname "$1")" && age-keygen | age -p -o "$1"' sh RECOVERY
   ```

3. **Cache your recovery identity's recipient.** The public half. Every secrets file is encrypted to it as well as to its host, and fleet secrets reads it from the cache, fleet/recovery.pub in your state folder (XDG_STATE_HOME), so that it never needs the passphrase for that. age asks for the passphrase once, if the cache is missing.

4. **Create your signing key.** An SSH key that signs every commit. Give it a passphrase, and load it into your agent with ssh-add when you work.

   ```
   $ ssh-keygen -t ed25519 -a 100 -C 'fleet signing' -f SIGNING-KEY
   ```

5. **Sign every commit in both checkouts.** Hosts accept only signed commits. These settings belong to the checkouts only, not your other repositories.

6. **Install the secrets repository's commit check.** Its pre-commit hook refuses anything under hosts/ or users/ that is not ciphertext. Git runs a hook as you, with your agent at hand, so what is installed is a copy you have read, not the repository's own file, which anyone who can push could change.

7. **Trust your key in fleet/allowed_signers.** Hosts check every fetched commit against this file. Adding a key is itself a commit, which must be signed by a key already trusted, if there is one.

8. **Keep your copy of the configuration repository's keys.** fleet and git check commits against your own copy of the keys, kept outside every checkout, not against fleet/allowed_signers, which a pushed commit could change. The first copy is the one you approve now; fleet trust signers takes later changes.

9. **Next.** With that, hosts can be enrolled.

   > Push the allowed_signers commit to fleet-manager's main if one was made, merging
   > locally: GitHub's merge buttons drop the signature. Then, for each host:
   > fleet host enroll \<host>

It needs `git`, `age`, `age-keygen`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
