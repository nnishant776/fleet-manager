<!-- Written by `fleetmgr docs` from tools/fleetmgr/flows; edit the flows, not this. -->

# fleetmgr host new

Write a new host's folder, publish it, print the install line, and set up its secrets from the enrollment file the installer shows.

fleetmgr host new sets up a machine that is to be installed from a private inventory, in one guided run.

Part 1, before the machine is installed: it asks what the host is (its platform, role and machine type,
how it boots, its disk and whether to encrypt it, its network, timezone and locale, and its accounts), each
answer also a flag. It writes the host's folder in the inventory: one default.nix, with the disk layout on,
which the installer lays out. It checks the host evaluates, commits, pushes and publishes its branch, then
checks the commits the install takes and prints the line to run on the machine booted from the NixOS
installer, as fleetmgr install prep does.

Part 2, when the installer shows the host's enrollment file: on a terminal it waits for you to copy the file
to the path it names; a later run takes it with --from. It enrolls the host in the secrets repository,
stores support's keys (--support-keys), each account's password (asked twice and hashed here; --no-password
for an account that logs in by key only) and login keys (--keys name=file), pushes the host's branch, and
registers the host's deploy key on the secrets repository and its configuration key on the inventory, both
read-only. Then you press Enter on the installer, which installs.

```
fleetmgr host new [HOST] [flags]

fleetmgr host new desk1 --flake inventory --repo secrets
fleetmgr host new desk1 --flake inventory --repo secrets --platform x86_64-linux --role developer \
    --machine-type desktop --boot uefi --disk /dev/nvme0n1 --network networkmanager \
    --account 'alice:Alice Liddell:admin' --support-keys ~/support.pub --from ~/desk1.enroll
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleetmgr repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | Its name in the inventory and its hostname: lowercase letters, digits and - |
| `--platform` | The platform, nixpkgs.hostPlatform: x86_64-linux or aarch64-linux |
| `--role` | The role, conf.role: developer, embedded, gaming, minimal, server, workstation |
| `--machine-type` | The machine type, conf.machineType: laptop, desktop, headless, vm |
| `--boot` | How it boots: uefi, with systemd-boot, or bios, with GRUB on its disk |
| `--disk` | The disk the installer lays out, such as /dev/nvme0n1; os-install -d can name another at install time, except on a BIOS host, whose GRUB installs to this one |
| `--encrypt` | Whether LUKS2 encrypts its disk, with a passphrase asked at install time (default true; --encrypt=false for a VM) |
| `--network` | The network backend: networkmanager or networkd |
| `--timezone` | Such as Europe/Berlin; by default the one the inventory's hosts share |
| `--locale` | Such as en_US.UTF-8; by default the one the inventory's hosts share |
| `--account` | An account, as name:Full Name, or name:Full Name:admin for an administrator; given once per account; may be given more than once |
| `--state-version` | The NixOS release it is installed with; by default the release the inventory's hosts build with |
| `--secrets-address` | The secrets repository's address, conf.secrets.repo.url, for the host; by default the one the inventory's hosts share |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleetmgr repo list), which means the tool's own clone of it; defaults to FLEETMGR_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--from` | The enrollment file the installer shows, copied here; without it, a run on a terminal waits for it |
| `--user` | The account the maintainer logs in to the host as; support by default |
| `--support-keys` | The SSH public keys, one per line, that log in to the support account; needed unless host/\<host> of the secrets repository holds them already |
| `--no-password` | An account that logs in by key only, and gets no password; given once per account; may be given more than once |
| `--keys` | An account's login keys, as name=file: the SSH public keys, one per line; given once per account; may be given more than once |
| `--register-with` | Run with each public key on stdin, instead of gh or the web page; mostly for tests |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Check the inventory.** The host is committed on top of the inventory's main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. Its layout says where hosts go. A host it has already is refused, unless an earlier run of this command added it.

4. **Write HOST's folder into the inventory.** One file, default.nix, from your answers, with the disk layout on: the installer lays the disk out from it. It is shown first, and written only once you agree.

5. **Check it evaluates in the inventory.** The host is evaluated as the hosts will see it, and every host still evaluates. An evaluation that fails says why, and leaves the folder for you to change.

6. **Commit it.** One commit that adds the host's folder. The repository's own git settings sign it.

7. **Push it.** Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first.

8. **Publish its branch.** The host fetches its own branch, host/HOST, not main. Only that branch is cut, checked to build from its own files alone, signed and pushed.

9. **Check the configuration commit.** It is what gets installed: the checkout's newest commit, which must be signed by a key in its fleet/allowed_signers, and pushed, since the installer fetches it.

10. **Check the fleet-manager commit the installer comes from.** bootstrap, fleetmgr and os-install come from fleet-manager. It is the commit the configuration's flake.lock pins, fetched with git and checked against the same signers file.

11. **Run this on the installer.** On the machine booted from the NixOS installer. Typing it is error-prone: set a password there with passwd, then ssh nixos@\<its address> from here and paste it. Before you answer ssh's question about the host key, compare the fingerprint it shows with the one the installer's own screen shows (ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub there): something else on the network could pose as it, and would get the disk passphrase you type.

12. **Take HOST's enrollment file.** The installer makes the host's identity first and shows its enrollment file: the public halves only, its age recipient and its two deploy keys. It is checked to be HOST's before anything is done with it.

13. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleetmgr setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

14. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

15. **Enroll HOST in the secrets repository.** fleetmgr secrets host-enroll creates its branch, host/HOST, with its empty file encrypted to it and to your recovery recipient, each step shown.

16. **Store support's keys.** The support account logs in with them once the host runs, which is how you reach it.

17. **Store the accounts' passwords.** Each account's password is asked for twice, hashed here, and only the hash is stored, encrypted to the host. An account given --no-password gets none.

18. **Store the accounts' login keys.** The keys --keys gives, one file per account, stored as each account's authorized keys.

19. **Push host/HOST.** The installer fetches the host's secrets from it, and the host accepts the commits only if a key it trusts signed them.

   ```
   $ cd REPO && git push origin host/HOST
   ```

20. **Register HOST's deploy key, read-only.** It is how HOST fetches the private secrets repository. Read-only: a host never writes to it.

21. **Register HOST's configuration key, read-only.** Its configuration comes over SSH, from a private repository, and this key is how HOST fetches it. Read-only, like the deploy key.

22. **Next.** What is left to do on the installer, and after the first boot.

It needs `git`, `nix`, `sops`, `age`, `ssh-keygen`, `mkpasswd` on the PATH; `fleetmgr doctor` says where to get them.
