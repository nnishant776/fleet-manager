<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet host enroll

Enroll a running host: identity, enrollment, deploy key, first sync.

For a host that already runs, and whose configuration in this repository sets
conf.secrets.repo.url. fleet logs in as its admin account through an agent that
holds one key only, switches the host to that configuration if it does not run
it yet, creates its identity, copies its enrollment file here and checks it,
enrolls it in the secrets repository and pushes, registers its deploy key
read-only, then syncs the host and checks it. Steps that are done already are
skipped, so it can be run again after a failure.

```
fleet host enroll [HOST] [flags]

fleet host enroll neerajpinew --key ~/.ssh/home_network_ssh_key --repo ~/Projects/fleet-secrets
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos, then github:nnishant776/fleet-manager |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--admin` | The account to log in as until the host has support; admin by default |
| `--user` | The account for the steps after the switch; support by default, which needs nothing forwarded |
| `--key` | A private key file; the only key the agent fleet starts holds and forwards |
| `--repo` | The fleet-secrets checkout to change; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--switch-to` | A flake reference; defaults to the newest commit of github:nnishant776/fleet-manager's main, once it is signed by a key in the checkout's fleet/allowed_signers |
| `--register-with` | Run with the public key on stdin, instead of gh or the web page; mostly for tests |
| `--sync` | Sync the host at the end and run fleet host check (default true) |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Start an agent that holds one key.** ADMIN's sudo checks its key through the forwarded agent. Only this agent is forwarded, never yours, which holds your signing key.

2. **Look at HOST.** What is done already decides which of the next steps run.

3. **Switch HOST to its fleet configuration.** It does not run a configuration with secrets yet. The switch runs as a service on the host, so it finishes even if this connection drops.

4. **Point /etc/nixos at the configuration repository.** Its sync fetches origin, which should be the repository its configuration names.

   ```
   $ SSH_AUTH_SOCK=AGENT ssh -A -o BatchMode=yes -o ConnectTimeout=10 ADMIN@ADDRESS 'sudo -n git -C /etc/nixos remote set-url origin CONFIG_URL'
   ```

5. **Create HOST's identity.** Its age key decrypts its secrets, its deploy key fetches them, and its configuration key fetches a private configuration. All are made on the host and never leave it; init makes only what is missing.

   ```
   $ SSH_AUTH_SOCK=AGENT ssh -A -o BatchMode=yes -o ConnectTimeout=10 ADMIN@ADDRESS 'sudo -n fleet-secrets-sync init --state /var/lib/fleet --host HOST'
   ```

6. **Copy its enrollment file here.** It holds the public halves only. Copied over SSH to a host whose key is known, so it cannot be swapped on the way; the checksums must match.

7. **Enroll HOST in the secrets repository.** fleet secrets host-enroll records its recipient, renders the rules and creates its empty file, each step shown.

8. **Push the secrets repository.** HOST fetches from GitHub; it accepts the commit only if it is signed by an allowed key.

   ```
   $ cd REPO && git push
   ```

9. **Register HOST's deploy key, read-only.** It is how HOST fetches the private secrets repository. Read-only: a host never writes to it.

10. **Register HOST's configuration key, read-only.** Its configuration comes over SSH, from a private repository, and this key is how HOST fetches it. Read-only, like the deploy key.

11. **Sync HOST.** Its first sync with secrets: fetch and verify them, then its configuration.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

12. **Check HOST.** fleet host check, as support with nothing forwarded: this also shows support works.

   ```
   $ fleet host check HOST --address ADDRESS --flake FLAKE
   ```

It needs `ssh`, `ssh-agent`, `ssh-add`, `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
