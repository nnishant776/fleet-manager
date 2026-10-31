# 11. Secrets runbooks

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 10. The fleet tool](fleet-tool.md) · [Contents](README.md) · [Appendix A. Configuration reference →](configuration.md)
<!-- /book:top -->

What to do in the secrets repository, and why: the maintainer's one-time
setup, onboarding a machine and a user, login and sudo keys, host secrets,
day-2 work, emergency access and troubleshooting. The examples use host
`laptop1`, users `alice` and `bob`, and the maintainer. How it all works is in
[Secrets](secrets.md); each command also has a page of its own in the
[runbooks](../tools/fleet/runbooks/README.md).

<!-- book:sections -->
In this chapter:

- [Maintainer: one-time setup](#maintainer-one-time-setup)
- [Onboarding a machine](#onboarding-a-machine)
- [Onboarding a user](#onboarding-a-user)
- [Login and sudo keys](#login-and-sudo-keys)
- [Host secrets](#host-secrets)
- [Day-2 runbooks](#day-2-runbooks)
- [Emergency access](#emergency-access)
- [Troubleshooting](#troubleshooting)
<!-- /book:sections -->

## Maintainer: one-time setup

`fleet setup` does steps 2 to 4 for you, showing each thing and asking before
it changes anything. Run it from a fleet-manager checkout:

```bash
fleet setup --config <configuration checkout> --repo <secrets checkout>
```

The steps by hand, if you want them:

**1. Tools.** `fleet` comes from fleet-manager and brings its own
dependencies. The manual steps need `age`, `sops`, `mkpasswd`, `jq` and `yq`:

```bash
nix shell nixpkgs#age nixpkgs#sops nixpkgs#mkpasswd nixpkgs#jq nixpkgs#yq-go
```

**2. Recovery identity.** Generate it straight into a passphrase-encrypted file,
so the private key never touches the disk in clear:

```bash
mkdir -p -m 700 ~/.fleet
age-keygen | age -p -o ~/.fleet/recovery.age      # asks for a passphrase
age -d ~/.fleet/recovery.age | age-keygen -y       # prints its recipient, age1…
```

Keep a copy of `recovery.age` offline and the passphrase in a vault. If both
are lost, no one can re-encrypt or add a host. The hosts keep working with
what they already have.

**3. Cache the recovery recipient.** Every file is encrypted to this
recipient as well as to its host. The tools read it from
`${XDG_STATE_HOME:-~/.local/state}/fleet/recovery.pub`, so they need no
passphrase for that. `fleet setup` writes it; by hand:

```bash
mkdir -p "${XDG_STATE_HOME:-$HOME/.local/state}/fleet"
age -d ~/.fleet/recovery.age | age-keygen -y > "${XDG_STATE_HOME:-$HOME/.local/state}/fleet/recovery.pub"
```

A command that encrypts and finds no cache stops with "run fleet setup".

**4. Signing key.** Use an SSH key, preferably a FIDO one
(`ssh-keygen -t ed25519-sk`). Configure the secrets checkout to sign every
commit and to check signatures:

```bash
git config gpg.format ssh
git config user.signingKey ~/.ssh/<signing key>.pub   # the private key must be in your agent
git config commit.gpgSign true
git config tag.gpgSign true
git config gpg.ssh.allowedSignersFile ~/.local/state/fleet/repos/github.com/<owner>/<config repo>/allowed_signers
```

That last file is your own copy of the configuration repository's keys, kept
outside every checkout. `fleet trust signers --flake <configuration checkout>`
makes it, after showing you the keys; `fleet repo add` (step 6) does the same
for its clones. Do not point git at a checkout's
`fleet/allowed_signers`. A pushed commit could add a key there and be signed
with it, and after a pull the file would vouch for that commit. The tools
refuse to run while git checks against a file inside a checkout.

Then read `.githooks/pre-commit` and copy it in:

```bash
cp .githooks/pre-commit .git/hooks/pre-commit
```

Git runs a hook as you, with your agent at hand. So it runs a copy you have
read, not the repository's own file, which anyone who can push could change.
When that file changes, read the change and copy it again. `fleet setup` shows
the hook, or what changed, and asks before copying. The tools refuse to change
anything while git runs the repository's own hook (`core.hooksPath` set to
`.githooks`).

The key must be in the configuration's `fleet/allowed_signers`, the file hosts
use as `conf.fleet.signing.allowedSignersFile`. It has one line per key,
`<label> namespaces="git" <key-type> <key>`; the label is free text and does
not have to be an email address. Adding a key there is itself a signed commit
to the configuration repository.

**5. Forge settings.** Keep the secrets repository **private**, and give push
access to the maintainer alone. Where the forge offers branch protection,
protect `main` and the `host/*` branches: require signed commits, forbid force
pushes and deletion (except for you, when you retire a host). GitHub has none
for a private repository on its free plan; the hosts still refuse an unsigned
commit, or one that does not follow the one they accepted last.
Hosts fetch over SSH, so the URL they use has the form
`git@github.com:example-org/secrets.git`.

There is nothing to register in a file: no maintainer line, no host list. With
the cache from step 3 in place, enrolling a host can start.

**6. The tools' own clones.** Give the tools a clone of each repository of
their own, so that they work on what is on the forge, checked, and not on a
checkout you are in the middle of editing:

```bash
fleet repo add      # three times: fleet-manager, then the inventory, then the secrets repository
```

Each time it says which repository it adds next and asks for its address
(fleet-manager's it knows). Each clone is named after the last part of its
address; `fleet repo list` shows the names. The secrets repository has no
`fleet/allowed_signers` of its own, so its commits are checked against the
inventory's keys, as the hosts check them. After that, give the name instead of
a path: `fleet secrets user-password alice laptop1 --repo secrets`. The
command fetches the clone first. A commit a command does not push stays in that
clone, and the command says how to push it. A path still works as before. More
on the clones is in
[The tools' own clones](signing-and-trust.md#the-tools-own-clones) in *Signing commits and trusting them*.

## Onboarding a machine

The complete flow for a new NixOS host, `laptop1`, with one user, `alice`.
macOS hosts get the sops wiring but no secrets sync yet, and their accounts
come from MDM.

### 1. Declare the host

In the configuration repository (the inventory, for a private fleet: see [A private fleet](private-fleet.md)):

```nix
# hosts/laptop1/default.nix
conf.hardware.disk = { enable = true; device = "/dev/nvme0n1"; encrypt = true; };
conf.fleet.repo.url = "git@github.com:example-org/inventory.git";   # the sync also refreshes secrets
conf.secrets.repo.url = "git@github.com:example-org/secrets.git";   # ref defaults to host/laptop1
conf.fleet.signing.allowedSignersFile = ../../fleet/allowed_signers;   # wherever the shared file lives
conf.fleet.support.keysFromSecrets = true;   # support's keys are in the secrets repository, step 4
conf.users.accounts.alice = { fullName = "Alice"; };   # password comes from the secrets repository
```

Optional: `conf.fleet.support.consolePassword = true` (needs
`support_password`, see [Host secrets](#host-secrets)),
`accounts.alice.secrets.sshKey = true` (needs `user-key`, step 4) and
`accounts.alice.secrets.authorizedKeys = true` (needs `user-keys`, step 4).

Commit and push the configuration. The installer builds the pushed
configuration.

### 2. Install: the host creates its identity

On your machine, in your inventory checkout, run

```bash
fleet install prep laptop1 --flake .
```

It checks the commit of the host's branch and the fleet-manager commit the
installer comes from, both signed by a key you trust, and prints one line.
Boot the stock NixOS installer on the machine, set a password there with
`passwd`, and `ssh nixos@<its address>` from your machine, first comparing the
host key fingerprint ssh shows with `ssh-keygen -lf
/etc/ssh/ssh_host_ed25519_key.pub` on the installer's screen. Paste the line.
Flags for the install go at its end: `--enroll-out /media/usb/laptop1.enroll`,
`--no-enroll`, `--identity FILE`. An install of a private host without that
line is refused.

After `nixos-install`, the install generates the host identity under
`/mnt/var/lib/fleet` (its own volume, so a later reinstall of `/` keeps it),
prints the enrollment file and a QR code of it, and **waits**. Leave it
waiting. Get the file to the maintainer's machine through a trusted channel:
scan the QR code, use the stick given to `--enroll-out`, or retype the two
keys into a file shaped like the example under [Trust model](secrets.md#trust-model) in *Secrets*.

### 3. Enroll the host (maintainer)

```bash
fleet host enroll laptop1 --flake <configuration checkout>
```

does this step's work for a host that already runs (not for one waiting at the
installer), asking before each change; it also registers the deploy keys,
pushes, and switches the host, and it checks that the host's branch holds
what step 4 sets, stopping before the switch if not. The pieces of this step,
if you run them by hand, in the secrets checkout:

```bash
git pull
fleet secrets host-enroll laptop1 --from laptop1.enroll
```

This creates the branch `host/laptop1` with an empty encrypted
`hosts/laptop1.yaml`, encrypted to the host and your recovery recipient, and
the enrollment file as `enroll`; commits it, signed; and prints the host's
secrets deploy key. Your clone stays on `main`. The command does not push.
Add the deploy key on the forge as a **read-only** deploy key of the secrets
repository: Settings → Deploy keys → Add deploy key, title `laptop1`, write
access **unchecked**. If the host's configuration lives in a private repository
(an SSH `conf.fleet.repo.url`), add the configuration key to that repository
the same way.

### 4. Give the host its secrets (maintainer)

Support's keys first. They are what lets the maintainer in after the switch, and
`fleet host enroll` stops before the switch if they are missing. Put the public
keys, one per line, in a file and run

```bash
fleet secrets user-keys support laptop1 --authorized-keys support.keys
```

It checks each key with `ssh-keygen`, stores them in `users/support/laptop1.yaml`
on `host/laptop1`, commits and pushes. Without them the host has no way in
until they are added and it syncs.

For every account the host declares with `secrets.password` (the default):

```bash
fleet secrets user-password alice laptop1      # asks twice, stores a yescrypt hash
```

If the account has `secrets.sshKey = true`:

```bash
fleet secrets user-key alice laptop1           # prints alice's new public key
```

Register the printed public key wherever alice needs it, such as her forge
account or servers. The private key exists in clear only in a tmpfs while the
command runs.

If the account has `secrets.authorizedKeys = true` (keys that may log in as
alice) or `secrets.sudoKeys = true` (a privileged account's sudo keys), see
[Login and sudo keys](#login-and-sudo-keys):

```bash
fleet secrets user-keys alice laptop1 --authorized-keys alice.keys
fleet secrets user-keys alice laptop1 --sudo-keys alice-sudo.keys
```

If the configuration declares any host secrets (e.g. `support_password`), add
them now, see [Host secrets](#host-secrets). Then push the branch (skip this
if `fleet host enroll` and `fleet host secret` already pushed it and you made
no other change):

```bash
git push origin host/laptop1
```

### 5. Finish the install

Press Enter at the waiting installer. It fetches the host's branch with the
deploy key, verifies the signature, keeps a snapshot of `laptop1`'s files and
checks that `hosts/laptop1.yaml` decrypts:

| Installer says | Meaning |
|---|---|
| `secrets in place` | Done. Reboot |
| code 10 | Fetch failed: the deploy key isn't registered, or the branch isn't pushed yet |
| code 11 | The tip of `host/laptop1` isn't signed by an allowed signer |
| code 15 | Fetched, but encrypted to another key: enrolled from the wrong file, or not pushed |

On any failure the host still boots and keeps retrying. Accounts that need a
password stay locked until the secrets arrive.

### 6. After first boot

```bash
ssh support@laptop1                                # the maintainer's support key, from users/support/laptop1.yaml
sudo journalctl -u fleet-bootstrap -u config-auto-update
sudo ls -l /var/lib/fleet                          # secrets -> snapshots/<hash of its manifest>
```

Alice logs in with the password from step 4. If `tpm2Unlock` is set, enroll
the TPM as `os-install` printed.

### Variants

- **Enroll later.** With `--no-enroll` at the end of the prep line, the install doesn't wait. The host boots with
  locked accounts and retries the fetch until it is enrolled. Fetch its
  enrollment file over SSH and pipe it in:
  `ssh support@laptop1 sudo cat /var/lib/fleet/laptop1.enroll | fleet secrets host-enroll laptop1 --from -`,
  then continue with step 4. On the host, `sudo fleet-identity --qr` shows the
  file again.
- **Reinstall, identity kept.** After a manual reinstall that kept the `fleet`
  volume, nothing changes. `os-install` erases it, so to carry the identity
  over, run `sudo fleet-identity --export > /media/usb/laptop1.identity` on the
  old install, then install with `--identity /media/usb/laptop1.identity` at
  the end of the prep line. No enrollment and no change to a repository or the
  forge is needed. Shred the file afterwards.
- **Reinstall, new identity.** An install from a new prep line writes a new
  enrollment file. Run `fleet secrets host-rekey laptop1 --from laptop1.enroll` and
  `git push origin host/laptop1`, add the new deploy key on the forge and
  **delete the old one**, then press Enter at the installer.

## Onboarding a user

To give `bob` an account on a host that is already enrolled. There are two ways
to keep his files, and they can be switched later.

### Files on the host's branch (the maintainer does it all)

1. **In the configuration:** add `conf.users.accounts.bob = { fullName = "Bob"; };` to
   `hosts/laptop1/` (plus `secrets.sshKey = true` if he needs a key from
   the secrets, and `secrets.authorizedKeys = true` for keys that may log in as
   him). Commit.
2. **In the secrets repository:** `fleet secrets user-password bob laptop1` and, if wanted,
   `fleet secrets user-key bob laptop1` and
   `fleet secrets user-keys bob laptop1 --authorized-keys bob.keys`. Register
   the printed public key where bob needs access. All of them commit to
   `host/laptop1`.
3. Push **both** repositories, the secrets first (`git push origin host/laptop1`;
   `user-keys` pushes for you). The order isn't critical: if a host's
   configuration needs `users/bob/laptop1.yaml` before the file exists, the
   host refuses to rebuild, logs `missing: /var/lib/fleet/secrets/users/bob/laptop1.yaml`, and
   changes nothing until the file arrives.

For a user on several hosts, repeat step 2 for each host. Each host gets its
own file, on its own branch, and its own SSH key. An SSH key added later works
the same way: set `accounts.bob.secrets.sshKey = true`, run
`fleet secrets user-key bob laptop1`, and push both; the sync refuses until
both exist.

Here the maintainer sets passwords. Either let the user type the password at
the `user-password` prompt, or choose one and hand it over through a secure
channel. Users can't change their own password on a fleet host, because the
hash from the secrets is reapplied at every activation. To change a password, run
`user-password` again.

### A branch of his own (bob changes his files himself)

1. Bob runs `fleet user init` once. It makes his age identity
   (`~/.fleet/user.age`, passphrase-encrypted), an SSH signing key and his
   enrollment file, `~/.fleet/bob.enroll` (`version: 1`, `user`,
   `age_recipient`, `signing_key`), which holds public keys only. He
   gives you the file through a channel you trust.
2. The maintainer, after the account is in the configuration and the hosts run a
   fleet-manager that reads user branches:
   `fleet secrets user-enroll bob --from bob.enroll`. It finds every host
   branch with a file for bob (add `--host` for more), moves them to
   `user/bob`, encrypted to bob as well, lists bob in each host's
   `hosts/<h>.yaml`, and pushes `user/bob` and then the host branches.
3. From the next sync on, each host takes bob's file from his branch. Bob now
   changes his files himself, signed with his own key, and the maintainer does
   nothing: `fleet user password laptop1` for a new password (`--all-hosts` for
   every host he has a file on), `fleet user keys laptop1 --add-authorized-keys
   new.pub` for a login key, and `fleet user secret set token laptop1` for a
   secret of his own. Each asks for his identity's passphrase, and for the SSH
   key's if it is not in his agent, and pushes `user/bob`; the host takes it at
   its next sync, with no rebuild.

**A secret of a user's own on a host.** Set `accounts.bob.secrets.own = true`.
The host then decrypts bob's whole file for bob alone, and `fleet-user-secret
token` run as bob prints the value he stored with `fleet user secret set token
laptop1`. It says so and exits 1 when there is no such secret. The whole
file also holds bob's password hash and keys, which only bob can read.

If one of his commits is unsigned, or fails another check, the hosts keep the
file they had and say so: see [Troubleshooting](#troubleshooting).

## Login and sudo keys

Set `accounts.bob.secrets.authorizedKeys = true` (and `secrets.sudoKeys = true`
for a privileged account), put the public keys in files, one per line, and run
`fleet secrets user-keys bob laptop1 --authorized-keys bob.keys --sudo-keys
bob-sudo.keys`. Every line is checked with `ssh-keygen` first, so a typo cannot
lock anyone out; the keys are stored in `users/bob/laptop1.yaml` (the file is made
if it is not there), and the branch is committed and pushed. A host picks them
up at its next sync with no rebuild. A file replaces the keys stored before,
so list every key that should stay; to change the list instead, use
`--add-authorized-keys FILE` and `--remove-authorized-keys FILE` (and
`--add-sudo-keys`, `--remove-sudo-keys`), which read the stored keys, add the
keys not there yet or remove the ones given, matching on key type and key
material (options such as `from=` and the comment do not matter), and keep the
other lines. A removal that would leave no key is refused, and a change that
changes nothing makes no commit.

A host with secrets takes its SSH keys from the secrets repository only: keys
in the configuration (`sshKeys`, `sudoSshKeys`, `fleet.support.sshKeys`, and
NixOS's own `users.users.<name>.openssh.authorizedKeys`, root's included) are a
build error there, support has to have `keysFromSecrets` on, and sshd does not
read `~/.ssh/authorized_keys`, which anything running as the account could add
a key to. Hosts without secrets keep keys in the configuration and the home
directory. To move a host over, add the keys to the secrets, turn the flag on
and remove them from the configuration in one change. Support's keys are set
the same way: `fleet secrets user-keys support laptop1 --authorized-keys
support.keys`, with `fleet.support.keysFromSecrets = true`. If the file or the
key is missing, the sync refuses the configuration, and `fleet host enroll`
stops before the switch with the command that adds it.

## Host secrets

`hosts/<host>.yaml` starts empty (`{}`). The easy way to set a secret is

```bash
fleet host secret laptop1 wifi_psk
```

which asks for the value (hidden), encrypts it into `hosts/laptop1.yaml` on
`host/laptop1`, commits, pushes the branch, and shows the lines the host's
configuration needs.

By hand, work in a temporary worktree of the host's branch, so the clone
stays on `main`. `SOPS_AGE_KEY_CMD` makes sops ask for the recovery passphrase:

```bash
git worktree add /tmp/laptop1 host/laptop1
cd /tmp/laptop1
export SOPS_AGE_KEY_CMD="age -d $HOME/.fleet/recovery.age"
sops hosts/laptop1.yaml                            # decrypts, opens $EDITOR, re-encrypts on save
git commit -am "laptop1: wifi_psk" && git push origin host/laptop1
cd - && git worktree remove /tmp/laptop1
```

To set a single key without an editor, pipe the value in so it never shows up
in the process list:

```bash
mkpasswd -m yescrypt | jq -R . | sops set --value-stdin hosts/laptop1.yaml '["support_password"]'
```

Names, groups and the names that are taken are under
[Host secrets](secrets.md#host-secrets) in *Secrets*.

## Day-2 runbooks

Each of these ends with `git push origin host/<host>` in the secrets repository
(unless the command pushed already), and with a push of the configuration where
the step changes it.

- **Rotate a password.** `fleet secrets user-password alice laptop1`. `passwd`
  on a fleet host does not last, since accounts are fixed by the
  configuration: a password changes in the secrets repository.
- **Rotate an SSH key.** `fleet secrets user-key alice laptop1`. Register the
  new public key and revoke the old one on the forge.
- **Rotate login or sudo keys.**
  `fleet secrets user-keys alice laptop1 --add-authorized-keys new.pub`, then
  `--remove-authorized-keys old.pub` (and `--sudo-keys`, `--add-sudo-keys`,
  `--remove-sudo-keys` for sudo); see [Login and sudo keys](#login-and-sudo-keys).
  For support, use the same command with `support`, or `fleet support-key
  rotate`, which does it and tests each step. For a user with a branch of
  their own, they run `fleet user keys` instead.
- **User leaves.** Remove `accounts.alice` from every host in the configuration
  and push first, so no host still needs her files. If her files are on the host
  branches, delete `users/alice/<host>.yaml` on each in a worktree and commit
  (signed), then push the branch. If she has a branch of her own, run
  `fleet secrets user-retire alice`: it removes her from every host's list and
  deletes `user/alice` in your clone, and you delete it on the forge
  (`git push origin --delete user/alice`). Revoke her public keys on the forge.
  The account disappears at the next sync.
- **Re-key a host** (reinstalled with a new identity).
  `fleet secrets host-rekey laptop1 --from laptop1.enroll` replaces the host's
  recipient with the new one in every file on its branch, keeps the recovery
  recipient, and replaces `enroll`. It also re-keys the files of the users the
  host lists, on their branches. Push the branches, register the new deploy
  key on the forge and delete the old one.
- **Retire a host, or it is lost or compromised.** Run
  `fleet secrets host-retire laptop1`. That deletes the branch in your clone
  and lists the rest: delete the branch on the forge
  (`git push origin --delete host/laptop1`), which takes it away from the
  host; delete the host's deploy keys on the forge, which takes effect at once;
  and remove the host's folder from the configuration. Then rotate what the host
  could read, which is only its own files: its users' passwords and keys **on
  laptop1**, and whatever was in `hosts/laptop1.yaml`. Their files for other
  hosts were never on it.
- **Recovery passphrase or file exposed.** Treat it as every host being
  compromised and rotate every secret. There is no command to change the
  maintainer recipient yet.

## Emergency access

The way in for an emergency is the secrets. It needs no rebuild and no change to
the configuration:

```bash
fleet secrets user-keys support laptop1 --add-authorized-keys emergency.pub
```

This commits and pushes `host/laptop1`. The host applies it at its next sync, by
activating the running system again. When you are done, take the key away:

```bash
fleet secrets user-keys support laptop1 --remove-authorized-keys emergency.pub
```

Both commits stay in the history, as the record of who had access and when. Use
`support`, or the account that needs the access. For a user with a branch of
their own, the same command works on their branch.

**Timing.** A host syncs once a day by default (`conf.fleet.autoUpdate.dates`,
`"04:00"`, plus up to 30 minutes of random delay). So an emergency key, and a
revocation, can take up to a day. Only someone already on the host can start the
sync sooner: `sudo systemctl start config-auto-update`. For hosts where a day is
too slow, set `conf.fleet.autoUpdate.dates = "hourly";` in the host's
configuration, once. A sync with nothing new is cheap (a fetch and a check, a few
seconds), and every secrets change, revocations included, then lands within
about an hour.

**When the host cannot sync** (no network, the forge is down):

- Support's console password, for login at the machine. It is
  `conf.fleet.support.consolePassword`, with the hash `support_password` in
  `hosts/<h>.yaml` (see [Host secrets](#host-secrets)).
- As a last resort, root on the host can write a key straight into
  `/run/secrets/users/<u>/authorized_keys`, the account's own file (mode 0400).
  It works at once and leaves no record. It is gone at the next activation or
  reboot.

**Keys in the configuration are not an emergency path** on a host with secrets.
They are a build error there, by design: keys live in one place.

## Troubleshooting

On the host, look at `journalctl -u config-auto-update` (the periodic sync) and
`journalctl -u fleet-bootstrap` (first boot). `fleet-secrets-sync` exit codes:

| Code | Meaning | Fix |
|---|---|---|
| 10 | Fetch failed | The deploy key isn't registered or was deleted, the URL is wrong, the branch `host/<host>` doesn't exist on the forge, or there's no network. The log says when the forge rejected the key |
| 11 | The tip of the host's branch isn't signed by an allowed signer | Sign it (in a worktree of the branch: `git commit --amend -S --no-edit`, then push), or add the signer to the configuration's `allowed_signers` |
| 12 | The configuration needs a file or key that's missing in the secrets | The log lists each `missing:`. Create it on the host's branch and push |
| 13 | Bad state directory | `/var/lib/fleet` must be owned by root with mode 0700 |
| 14 | The host identity is missing | The host lost `/var/lib/fleet`. Re-key it (see [Variants](#variants)) |
| 15 | The host file doesn't decrypt with the host's key | Enrollment isn't finished, or a re-key isn't pushed |
| 16 | The tip of the branch doesn't follow the commit the host accepted last (a rollback) | Find out why the history moved. To accept it on purpose, remove `/var/lib/fleet/accepted/host` on the host (`accepted/user-<u>` for a user's branch) |

If a user's branch fails a check (an unsigned commit, a file not encrypted to
exactly the user, this host and the recovery recipient, a commit that does not
follow the last accepted one), the host keeps the file it had for that user and
the others carry on. The user is named in the notice shown at every login, in
`fleet-sync-status` and in `fleet host check`, until a good commit fixes it.
Look at `git log --format='%h %G? %s' user/<u>`; if the user's commit is the
problem, they fix it on their branch, or the maintainer replaces it with a
signed one.

On the maintainer's side, a command that stops with "run fleet setup" has no
`recovery.pub` cache, and one that refuses a branch found its tip unsigned or
signed by a key you do not trust. One that says a branch "does not follow" the
commit you trusted last saw it go back to an older commit or be rewritten. If
your own checkout is merely behind, pull it. Otherwise review it with
`fleet repo sync secrets`, which lists the commits on each branch since
the ones you trusted and who signed them, and accepts them when you agree; a
history rewritten on purpose takes `--accept-rewrite <branch>`, and you type
the branch's name to confirm. A user's branch signed by the user's own key is
left to the secrets commands, which check it against that key.

`git log --format='%h %G? %s' host/laptop1` shows the signature status of
recent commits on a host's branch: `G` is good, `N` is unsigned.

<!-- book:bottom -->
---

[← 10. The fleet tool](fleet-tool.md) · [Contents](README.md) · [Appendix A. Configuration reference →](configuration.md)
<!-- /book:bottom -->
