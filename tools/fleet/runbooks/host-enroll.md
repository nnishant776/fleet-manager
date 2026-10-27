<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet host enroll

Enroll a running host: identity, enrollment, deploy key, first sync.

For a host that already runs, and whose configuration in this repository sets
conf.secrets.repo.url. fleet logs in as its admin account through an agent that
holds one key only, switches the host to that configuration if it does not run
it yet or runs another repository's, creates its identity, copies its enrollment file here and checks it,
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
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--admin` | The account to log in as until the host has support; admin by default |
| `--user` | The account for the steps after the switch; support by default, which needs nothing forwarded |
| `--key` | A private key file; the only key the agent fleet starts holds and forwards |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--switch-to` | A flake reference; defaults to the newest commit of github:nnishant776/fleet-manager's main, once it is signed by a key in the checkout's fleet/allowed_signers |
| `--register-with` | Run with the public key on stdin, instead of gh or the web page; mostly for tests |
| `--sync` | Sync the host at the end and run fleet host check (default true) |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Start an agent that holds one key.** ADMIN's sudo checks its key through the forwarded agent. Only this agent is forwarded, never yours, which holds your signing key.

4. **Look at HOST.** What is done already decides which of the next steps run.

5. **Choose the commit to switch HOST to.** It does not run a configuration with secrets from this repository yet. Unless --switch-to names one, it gets a commit a key you trust has signed, as its own sync will take no other: for a private configuration the one fleet install prep would install, otherwise the newest commit of main.

6. **Create HOST's identity.** Its age key decrypts its secrets, its deploy key fetches them, and its configuration key fetches a private configuration. All are made on the host and never leave it; init makes only what is missing. A host without fleet's tools yet runs init from the fleet-manager commit checked above, before its switch, which for a private configuration needs the key.

7. **Copy its enrollment file here.** It holds the public halves only. Copied over SSH to a host whose key is known, so it cannot be swapped on the way; the checksums must match.

8. **Enroll HOST in the secrets repository.** fleet secrets host-enroll creates its branch, host/HOST, with its empty file encrypted to it and to your recovery recipient, each step shown.

9. **Push host/HOST.** HOST fetches its branch from GitHub; it accepts the commit only if it is signed by an allowed key.

   ```
   $ cd REPO && git push origin host/HOST
   ```

10. **Register HOST's deploy key, read-only.** It is how HOST fetches the private secrets repository. Read-only: a host never writes to it.

11. **Register HOST's configuration key, read-only.** Its configuration comes over SSH, from a private repository, and this key is how HOST fetches it. Read-only, like the deploy key.

12. **Check its configuration fits HOST as it is.** Without a reinstall, the configuration must describe the disks the machine has and boot the way it boots: a root or boot filesystem it does not have, or a UEFI boot loader on a machine without UEFI, would leave it unbootable after the switch.

13. **Check host/HOST holds the secrets the configuration declares.** After the switch the host's sync refuses a snapshot that lacks a secret the configuration declares, such as the keys of an account that takes them from its secrets. The branch is read as it is in git, without decrypting; if something is missing, this stops before the switch with the command that adds it.

14. **Warn that /etc/nixos on HOST will be replaced.** /etc/nixos belongs to the fleet's configuration: nothing it holds now is kept. This step changes nothing; it shows what is there and asks, before the switch, so that you can copy what you need first.

15. **Switch HOST to its fleet configuration.** The switch runs as a service on the host, so it finishes even if this connection drops. A private configuration is fetched with the host's configuration key. /etc/nixos is replaced after it.

16. **Make /etc/nixos the configuration repository's checkout.** The sync owns /etc/nixos and fetches the repository the configuration names. What was there is deleted, and the first-boot clone, fleet-bootstrap, makes the checkout at the commit the host runs and records it as the first the machine accepted.

17. **Sync HOST.** Its first sync with secrets: fetch and verify them, then its configuration.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

18. **Check HOST.** fleet host check, as support with nothing forwarded: this also shows support works.

   ```
   $ fleet host check HOST --address ADDRESS --flake FLAKE
   ```

It needs `ssh`, `ssh-agent`, `ssh-add`, `git`, `yq`, `sops`, `age` on the PATH; `fleet doctor` says where to get them.
