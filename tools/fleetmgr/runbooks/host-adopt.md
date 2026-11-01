<!-- Written by `fleetmgr docs` from tools/fleetmgr/flows; edit the flows, not this. -->

# fleetmgr host adopt

Take in a running NixOS machine the inventory does not have, or the owner a personal machine made at its first boot.

fleetmgr host adopt takes in what a machine holds. Which case it is follows from the inventory.

A machine the inventory does not have, which runs NixOS but no fleet configuration, is taken in whole.
fleetmgr records its host key once you have compared the fingerprint with its console, and logs in as an
admin account (--admin) through an agent that holds one key (--key); that account's sudo may take the key,
a rule, or its password, asked for once and held in memory. It reads the machine: how it boots, its network,
timezone and locale, the release it was installed with, its hardware configuration, its /etc/nixos, and the
people's accounts. Then it writes the host's folder in the inventory: its own configuration copied as it is
(--copy-config), imported by a default.nix that adds what the fleet needs, or a minimal skeleton
(--skeleton); either way, what of the machine's /etc/nixos is its own goes into previous/, with previous.md
saying what the rest was: a checkout of fleet-manager keeps only the host's folder and its local changes, a flake
without the machine's host keeps nothing, and a file that looks like a secret is left out. It checks that the
host evaluates, and if it does not, says why and leaves the folder for you to change, so that a second run
goes on from it.
Then it commits, pushes, and publishes the host's branch.

Then the machine is brought under the fleet, as fleetmgr host enroll brings a host: its identity, its
branch of the secrets repository, its keys registered. The passwords and login keys of the accounts its
configuration takes from the secrets are stored there as the machine has them, so that everyone keeps
theirs, with support's from --support-keys. After the checks of a switch, and a list of the services that
would stop, it switches for a trial (--trial-minutes): unless support logs in to the new system, the
machine goes back by itself. Then /etc/nixos becomes the fleet's checkout, and the host syncs.

A personal machine (conf.fleet.personal) lets its owner make their own account at the first boot, and
keeps that account out of the configuration. This starts bringing it in. fleetmgr logs in to the host as
support and reads who the owner is (/var/lib/fleet/owner), their full name and home, their password hash
(never shown, and passed on standard input only) and the login keys in their ~/.ssh/authorized_keys.
It stores the hash and the keys in the secrets repository (fleetmgr secrets user-password --hash-stdin and
user-keys), after fleetmgr secrets user-enroll when you give the owner's enrollment file with --from, which
is for an owner who will change their own password and keys later (fleetmgr user init makes the file).
It changes nothing in the inventory. It prints the declaration for you to put into the host's own
default.nix, and the commands that finish: publish with a sync, and check the host. The owner keeps
their uid, files and password: NixOS keeps an existing account's ids when it becomes declared.

```
fleetmgr host adopt [HOST] [flags]

fleetmgr host adopt laptop3 --address 192.168.1.20 --admin alice --skeleton --flake inventory --repo secrets
fleetmgr host adopt laptop1 --flake inventory --repo secrets
fleetmgr host adopt laptop1 --from ann.enroll --flake inventory --repo secrets
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleetmgr repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A personal machine of the inventory, whose owner is adopted, or the name a machine the inventory does not have yet takes there |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--user` | The account fleetmgr logs in to a fleet host as; support by default, which has sudo without a password, so nothing needs forwarding |
| `--admin` | An account with sudo on the machine, to read it as; admin by default |
| `--key` | A private key file; the only key the agent fleetmgr starts holds and forwards |
| `--host-key` | SHA256:…, as ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub prints it on the host's console; only needed when this machine does not know the host's key yet and cannot ask |
| `--admin-password-stdin` | read the admin-password from standard input, instead of being asked. Only needed when the admin account's sudo asks for its password; asked for then, or read from --admin-password-stdin. It is kept in memory only, and passed to sudo on the host on stdin |
| `--copy-config` | Start the host's folder from the machine's own /etc/nixos, unchanged in previous/, which a default.nix imports |
| `--skeleton` | Start the host's folder from a minimal default.nix and the machine's hardware-configuration.nix; what of its /etc/nixos is its own is kept in previous/, for reference |
| `--role` | conf.role: developer, embedded, gaming, minimal, server, workstation; guessed from the machine when not given |
| `--machine-type` | conf.machineType: laptop, desktop, headless, vm; guessed from the machine when not given |
| `--grub-device` | For a machine that boots GRUB with BIOS; by default the disk that holds /boot, or /, by its /dev/disk/by-id name |
| `--secrets-address` | conf.secrets.repo.url for the host; by default the one the inventory's hosts share |
| `--state-version` | For a skeleton: the NixOS release it was installed with, such as 24.11; read from the configuration it was built from when not given |
| `--keep-previous` | Keep its /etc/nixos in previous/ even when that is more than 50 files or 1 MiB |
| `--support-keys` | SSH public keys, one per line, that log in to the support account on the host once it runs the fleet's configuration; needed unless host/\<host> of the secrets repository holds them already |
| `--trial-minutes` | After the switch, fleetmgr must log in to the new system as support within this time, or the host goes back to the system it ran before; 5 by default |
| `--switch-to` | A flake reference; by default the commit of the inventory fleetmgr install prep would install |
| `--register-with` | Run with each public key on stdin, instead of gh or the web page; mostly for tests |
| `--sync` | Sync the host at the end and run fleetmgr host check (default true) |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleetmgr repo list), which means the tool's own clone of it; defaults to FLEETMGR_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--from` | The owner's enrollment file (fleetmgr user init), when they will manage their own password and keys later; none: they ask you |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Read how HOST is configured.** Only a personal machine has an owner to adopt. Whether the owner is an administrator is read from its configuration, and carried over. Nothing is written.

4. **Reach HOST.** As USER, with no agent forwarded, and only to a host whose key this machine already knows.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'id -un; hostname'
   ```

5. **Read the owner from HOST.** As support, with sudo: who made the account (/var/lib/fleet/owner), what getent says of it, the password hash from the shadow file and the login keys in the owner's home. The hash is kept in memory only, never shown, and passed on standard input.

6. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleetmgr setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

7. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

8. **Enroll the owner in the secrets repository.** With their enrollment file the owner gets a branch of their own, user/\<owner>, and keeps their password and keys there with fleetmgr user. This is fleetmgr secrets user-enroll, run for the owner and this host.

9. **Store the owner's password hash.** As it is on the host: the owner keeps the password they have. This is fleetmgr secrets user-password --hash-stdin.

10. **Store the owner's login keys.** The keys of their ~/.ssh/authorized_keys, which the configuration will read from the secrets instead. This is fleetmgr secrets user-keys --authorized-keys.

11. **Push the secrets.** HOST fetches them from the forge on its next sync, and accepts the commits only if a key it trusts signed them. What the commands above left unpushed goes now.

12. **Next.** The declaration to add to the inventory, and the commands that finish.

13. **Check the inventory.** The host is committed on top of the inventory's main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. Its layout says where hosts go. A host it has already, from an earlier run, is not written again.

14. **Know HOST's host key.** Every login checks it, so that nothing between here and there can answer in the host's place. A key this machine does not know yet is fetched, and recorded only once its fingerprint matches the one on the host's console.

15. **Start an agent that holds one key.** ADMIN logs in with it. Only this agent is forwarded, never yours, which holds your signing key.

16. **Check ADMIN's sudo.** Without a password, by the key in the agent or a rule, nothing more is needed. Otherwise its password is asked for once, checked with a single attempt, kept in memory only, and handed to sudo on the host on stdin.

17. **Read HOST.** What its folder is written from: the platform, how it boots, its network, timezone and locale, the release it was installed with, its hardware configuration, its /etc/nixos, and the people's accounts, with their password hashes (kept in memory only, never shown) and login keys.

18. **Choose how HOST's folder starts.** Its own configuration, copied as it is, keeps everything it does; a skeleton keeps only what fleetmgr read, and the fleet's roles do the rest. Either way what of its /etc/nixos is its own goes into previous/, and previous.md says what the rest was.

19. **Write HOST's folder into the inventory.** Each file is shown first. If a later step fails, the folder stays, for you to look at or change; run this again to go on from it.

20. **Check it evaluates in the inventory.** The host is evaluated as the hosts will see it: the address it fetches, its branch, the signers file it uses, and that every host still evaluates. An evaluation that fails says why, with a hint where one is known, and leaves the folder as it is.

21. **Commit it.** One commit that adds the host's folder. The repository's own git settings sign it.

22. **Push it.** Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first. What you push is the commit you trust on main from then on.

23. **Publish its branch.** The host fetches its own branch, host/HOST, not main. Only that branch is cut, checked to build from its own files alone, signed and pushed.

24. **Look at HOST.** What is done already decides which of the next steps run.

25. **Choose the commit to switch HOST to.** It does not run a configuration with secrets from this repository yet. Unless --switch-to names one, it gets a commit a key you trust has signed, as its own sync will take no other: for a private configuration the one fleetmgr install prep would install, otherwise the newest commit of main.

26. **Create HOST's identity.** Its age key decrypts its secrets, its deploy key fetches them, and its configuration key fetches a private configuration. All are made on the host and never leave it; init makes only what is missing. A host without fleetmgr's tools yet runs init from the fleet-manager commit checked above, before its switch, which for a private configuration needs the key.

27. **Copy its enrollment file here.** It holds the public halves only. Copied over SSH to a host whose key is known, so it cannot be swapped on the way; the checksums must match.

28. **Enroll HOST in the secrets repository.** fleetmgr secrets host-enroll creates its branch, host/HOST, with its empty file encrypted to it and to your recovery recipient, each step shown.

29. **Push host/HOST.** HOST fetches its branch from GitHub; it accepts the commit only if it is signed by an allowed key.

   ```
   $ cd REPO && git push origin host/HOST
   ```

30. **Register HOST's deploy key, read-only.** It is how HOST fetches the private secrets repository. Read-only: a host never writes to it.

31. **Register HOST's configuration key, read-only.** Its configuration comes over SSH, from a private repository, and this key is how HOST fetches it. Read-only, like the deploy key.

32. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleetmgr setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

33. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

34. **Store the accounts' passwords and keys.** Each account the host's configuration takes its password or login keys from the secrets for gets them from the machine as they are: its password hash, never shown, and the keys in its authorized_keys. Everyone keeps their password and keys.

35. **Store support's keys.** The support account logs in with them once the host runs the fleet's configuration, and fleetmgr confirms the new system with them.

36. **Push the secrets.** HOST fetches them before its switch, and accepts the commits only if a key it trusts signed them. What the steps above left unpushed goes now.

37. **Check its configuration fits HOST as it is.** Without a reinstall, the configuration must describe the disks the machine has and boot the way it boots: a root or boot filesystem it does not have, or a UEFI boot loader on a machine without UEFI, would leave it unbootable after the switch.

38. **Check host/HOST holds the secrets the configuration declares.** After the switch the host's sync refuses a snapshot that lacks a secret the configuration declares, such as the keys of an account that takes them from its secrets. The branch is read as it is in git, without decrypting; if something is missing, this stops before the switch with the command that adds it.

39. **Warn that /etc/nixos on HOST will be replaced.** /etc/nixos belongs to the fleet's configuration: nothing it holds now is kept. This step changes nothing; it shows what is there and asks, before the switch, so that you can copy what you need first.

40. **Fetch HOST's secrets before the switch.** The new system takes its secrets, support's keys among them, from the host's snapshot of the secrets repository, which only its sync makes. So the host fetches, verifies and decrypts them now, as its sync will, with the signers file of the configuration it is switched to.

41. **Build HOST's new system.** On the host, as a unit, so that a dropped connection does not stop it. Nothing changes on the host yet.

42. **List what stops running on HOST.** A service that runs on the machine now and has no unit in the new system stops at the switch: the network, SSH or a database should not go unnoticed. Nothing changes here; with such services, it asks before going on.

43. **Try the new system on HOST, with a way back.** A timer is armed first: unless fleetmgr confirms the new system within TRIAL-MINUTES minutes, the host goes back to the one it runs now. Then the new system is activated, without becoming the boot default, so a reboot goes back as well.

44. **Confirm HOST works on its new system.** As USER, with nothing forwarded, as every step after this one logs in: if it can, the timer is stopped and the new system stays.

45. **Make the new system HOST's boot default.** It becomes the system profile's newest generation and the boot loader's default, as a plain switch would have made it.

46. **Make /etc/nixos the configuration repository's checkout.** The sync owns /etc/nixos and fetches the repository the configuration names. What was there is deleted, and the first-boot clone, fleetmgr-bootstrap, makes the checkout at the commit the host runs and records it as the first the machine accepted.

47. **Sync HOST.** Its first sync with secrets: fetch and verify them, then its configuration.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

48. **Check HOST.** fleetmgr host check, as support with nothing forwarded: this also shows support works.

   ```
   $ fleetmgr host check HOST --address ADDRESS --flake FLAKE
   ```

49. **Next.** What HOST is now, and what is left to you.

It needs `git`, `nix`, `tar`, `ssh`, `ssh-agent`, `ssh-add`, `ssh-keyscan`, `sops`, `age`, `ssh-keygen` on the PATH; `fleetmgr doctor` says where to get them.
