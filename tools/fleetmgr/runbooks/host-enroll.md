<!-- Written by `fleetmgr docs` from tools/fleetmgr/flows; edit the flows, not this. -->

# fleetmgr host enroll

Enroll a running host: identity, enrollment, deploy key, first sync.

For a host that already runs, and whose configuration in this repository sets
conf.secrets.repo.url. fleetmgr records the host's key, once you have compared its
fingerprint with the host's console, and logs in as its admin account through an agent
that holds one key only. The admin's sudo may take that key, a rule, or its password, which
is asked for once and held in memory. fleetmgr switches the host to that configuration if it
does not run it yet or runs another repository's, creates its identity, copies its
enrollment file here and checks it, enrolls it in the secrets repository and pushes,
registers its deploy key read-only, then syncs the host and checks it. Steps that are
done already are skipped, so it can be run again after a failure.

The switch is a trial. The host fetches its secrets first, so that the new system has
support's keys. The new system is activated with a timer armed that brings back the one
before after --trial-minutes, and it stays, as the boot default, only once fleetmgr has
logged in to it as support. A configuration that cuts the host off so undoes itself.

```
fleetmgr host enroll [HOST] [flags]

fleetmgr host enroll pi1 --key ~/.ssh/admin_key --repo ~/Projects/secrets
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleetmgr repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--admin` | The account with sudo to log in as until the host has support; admin by default |
| `--user` | The account for the steps after the switch; support by default, which needs nothing forwarded |
| `--key` | A private key file; the only key the agent fleetmgr starts holds and forwards |
| `--host-key` | SHA256:…, as ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub prints it on the host's console; only needed when this machine does not know the host's key yet and cannot ask |
| `--admin-password-stdin` | read the admin-password from standard input, instead of being asked. Only needed when the admin account's sudo asks for its password; asked for then, or read from --admin-password-stdin. It is kept in memory only, and passed to sudo on the host on stdin |
| `--trial-minutes` | After the switch, fleetmgr must log in to the new system as support within this time, or the host goes back to the system it ran before; 5 by default |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleetmgr repo list), which means the tool's own clone of it; defaults to FLEETMGR_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--switch-to` | A flake reference; defaults to the newest commit of github:nnishant776/fleet-manager's main, once it is signed by a key in the checkout's fleet/allowed_signers |
| `--register-with` | Run with the public key on stdin, instead of gh or the web page; mostly for tests |
| `--sync` | Sync the host at the end and run fleetmgr host check (default true) |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Know HOST's host key.** Every login checks it, so that nothing between here and there can answer in the host's place. A key this machine does not know yet is fetched, and recorded only once its fingerprint matches the one on the host's console.

4. **Start an agent that holds one key.** ADMIN's sudo may check its key through the forwarded agent. Only this agent is forwarded, never yours, which holds your signing key.

5. **Check ADMIN's sudo.** Without a password, by the key in the agent or a rule, nothing more is needed. Otherwise its password is asked for once, checked with a single attempt, kept in memory only, and handed to sudo on the host on stdin.

6. **Look at HOST.** What is done already decides which of the next steps run.

7. **Choose the commit to switch HOST to.** It does not run a configuration with secrets from this repository yet. Unless --switch-to names one, it gets a commit a key you trust has signed, as its own sync will take no other: for a private configuration the one fleetmgr install prep would install, otherwise the newest commit of main.

8. **Create HOST's identity.** Its age key decrypts its secrets, its deploy key fetches them, and its configuration key fetches a private configuration. All are made on the host and never leave it; init makes only what is missing. A host without fleetmgr's tools yet runs init from the fleet-manager commit checked above, before its switch, which for a private configuration needs the key.

9. **Copy its enrollment file here.** It holds the public halves only. Copied over SSH to a host whose key is known, so it cannot be swapped on the way; the checksums must match.

10. **Enroll HOST in the secrets repository.** fleetmgr secrets host-enroll creates its branch, host/HOST, with its empty file encrypted to it and to your recovery recipient, each step shown.

11. **Push host/HOST.** HOST fetches its branch from GitHub; it accepts the commit only if it is signed by an allowed key.

   ```
   $ cd REPO && git push origin host/HOST
   ```

12. **Register HOST's deploy key, read-only.** It is how HOST fetches the private secrets repository. Read-only: a host never writes to it.

13. **Register HOST's configuration key, read-only.** Its configuration comes over SSH, from a private repository, and this key is how HOST fetches it. Read-only, like the deploy key.

14. **Check its configuration fits HOST as it is.** Without a reinstall, the configuration must describe the disks the machine has and boot the way it boots: a root or boot filesystem it does not have, or a UEFI boot loader on a machine without UEFI, would leave it unbootable after the switch.

15. **Check host/HOST holds the secrets the configuration declares.** After the switch the host's sync refuses a snapshot that lacks a secret the configuration declares, such as the keys of an account that takes them from its secrets. The branch is read as it is in git, without decrypting; if something is missing, this stops before the switch with the command that adds it.

16. **Warn that /etc/nixos on HOST will be replaced.** /etc/nixos belongs to the fleet's configuration: nothing it holds now is kept. This step changes nothing; it shows what is there and asks, before the switch, so that you can copy what you need first.

17. **Fetch HOST's secrets before the switch.** The new system takes its secrets, support's keys among them, from the host's snapshot of the secrets repository, which only its sync makes. So the host fetches, verifies and decrypts them now, as its sync will, with the signers file of the configuration it is switched to.

18. **Build HOST's new system.** On the host, as a unit, so that a dropped connection does not stop it. Nothing changes on the host yet.

19. **Try the new system on HOST, with a way back.** A timer is armed first: unless fleetmgr confirms the new system within TRIAL-MINUTES minutes, the host goes back to the one it runs now. Then the new system is activated, without becoming the boot default, so a reboot goes back as well.

20. **Confirm HOST works on its new system.** As USER, with nothing forwarded, as every step after this one logs in: if it can, the timer is stopped and the new system stays.

21. **Make the new system HOST's boot default.** It becomes the system profile's newest generation and the boot loader's default, as a plain switch would have made it.

22. **Make /etc/nixos the configuration repository's checkout.** The sync owns /etc/nixos and fetches the repository the configuration names. What was there is deleted, and the first-boot clone, fleetmgr-bootstrap, makes the checkout at the commit the host runs and records it as the first the machine accepted.

23. **Sync HOST.** Its first sync with secrets: fetch and verify them, then its configuration.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

24. **Check HOST.** fleetmgr host check, as support with nothing forwarded: this also shows support works.

   ```
   $ fleetmgr host check HOST --address ADDRESS --flake FLAKE
   ```

It needs `ssh`, `ssh-agent`, `ssh-add`, `ssh-keygen`, `ssh-keyscan`, `git`, `sops`, `age` on the PATH; `fleetmgr doctor` says where to get them.
