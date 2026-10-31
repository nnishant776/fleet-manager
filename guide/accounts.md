# Accounts and homes

Who can log in, and how their homes are built: sudo with an SSH key instead of
a password, how a user's home is assembled, personal machines whose owner
makes their own account at the first boot, and what to know about accounts on
NixOS and macOS. The account options are under
[`conf.users`](configuration.md#confusers) in *Configuration reference*. On a
host with secrets, passwords and keys come from the secrets repository, as
[Secrets](secrets.md) explains.

## Sudo with an SSH key

This helps when an account has no password, on a machine you reach over SSH. List the keys that may unlock sudo:

```nix
conf.users.accounts.admin = {
  sshKeys     = [ "ssh-ed25519 AAAA… login key" ];
  sudoSshKeys = [ "ssh-ed25519 AAAA… sudo key" ];   # may be the same key
};
```

Then connect with your agent forwarded:

```sh
ssh -A admin@host
```

Or set it once in `~/.ssh/config`:

```
Host myhost
  ForwardAgent yes
  IdentityFile ~/.ssh/id_ed25519
```

How it behaves:

- sudo checks the key held by your agent against `sudoSshKeys`, which are kept in `/etc/ssh/sudo_keys.d/<user>` (owned by root).
- Login keys in `sshKeys` do not unlock sudo unless they are listed in `sudoSshKeys` too, so the sudo key can be a separate one that you load only when you need it. Keys in `~/.ssh/authorized_keys` never count.
- Hardware security keys (`sk-ssh-ed25519`) do not work for sudo. The PAM module that checks the key only knows RSA, ECDSA and Ed25519 keys.
- Without one of these keys, or without the agent, sudo asks for the password as before. `sudo -n`, as scripts use it, works with the key too.
- Only sudo changes. Console login and `su` still need the password, and root login stays off.
- On a host with secrets the keys come from the secrets repository, not the configuration (`sudoSshKeys` is an error there): set `secrets.sudoKeys = true` and put the keys, one per line, in `sudo_keys` in the account's file for the host (`fleet secrets user-keys admin host --sudo-keys FILE`). sudo reads them at each use from `/run/secrets/users/<user>/sudo_keys`, which is root's, mode 0400, so a change needs no rebuild and the account itself cannot add a key. It works the same way as `sudoSshKeys` otherwise, and is an error on an unprivileged account.
- Other users on the machine cannot use your agent. Root can, and so can any program running as you. While you are connected, they can use it to run sudo, or to log in to other machines with your key. Forward your agent only to machines you trust.

To limit that, add the key to your agent with a confirmation, a time limit, or both:

```sh
ssh-add -c ~/.ssh/id_ed25519          # ask each time the key is used
ssh-add -t 10m ~/.ssh/id_ed25519      # remove the key from the agent after 10 minutes
ssh-add -c -t 10m ~/.ssh/id_ed25519   # both
```

With `-c`, every sudo shows a prompt on the machine you connected from, and
nothing happens until you accept it there. This needs an askpass program on
that machine. macOS does not ship one, so install and set one up first. With
`-t`, the key leaves the agent when the time runs out, and you add it again
the next time you need sudo.

## How a home is assembled

A user's home configuration is assembled from the baseline in `modules/user/`,
anything in `users/<username>/default.nix`, their `homeConfig`, and — only for a
user with `selfManagedHome` — their own `~/.config/home-manager/home.nix`.

## Personal machines

A personal machine is handed out to one person, who makes their own account at
the first boot, with no help from the maintainer. Set
`conf.fleet.personal.enable = true` on a host that has `conf.fleet.repo.url`.

At the first boot the console shows a short setup instead of the login prompt.
It says what the machine is, offers to join a Wi-Fi network if there is none,
and asks for a username, a full name and a password (twice). It makes the
account, writes the name to `/var/lib/fleet/owner`, and the login prompt
follows. It does not run again, after a reboot or a sync.

The owner manages their own account and their network: Wi-Fi goes through
NetworkManager, and the passwords stay on the machine. The owner does not manage
the host's secrets. The maintainer keeps the system updated, through signed
commits as on any fleet host, and keeps the support account. By default the
owner is an administrator (`ownerPrivileged`), which means the account has root
and can change anything on the machine, support access included; every sync
repairs it. With `ownerPrivileged = false` the owner has an ordinary account
that can join and switch networks and nothing more.

Accounts on a personal machine are mutable: a password declared in the
configuration applies only when its account is created, and an account made by
hand stays. SSH logins of accounts the configuration declares still use only
their keys from the secrets repository; the owner's account also reads
`~/.ssh/authorized_keys`. The owner's account is not in the inventory. Later the
maintainer can adopt the owner into it; that is a separate step.

## Things to know

**A home has exactly one owner.** By default the organisation owns it and a
system rebuild activates it, in which case the user's own
`~/.config/home-manager/home.nix` is not read at all — silently. Personal
configuration for such a user belongs in `users/<username>/`. Setting
`selfManagedHome` moves ownership to the user, who then activates it with
`home-switch`; the system stops doing so. Do not try to arrange both: two Home
Manager generations over one home directory remove each other's files on every
activation.

**A self-managed home that falls behind is flagged loudly.** Every home
generation carries the flake revision it was built from
(`~/.config/org/revision`), and the system carries its own in
`/etc/org/revision`. After every system switch, at every graphical login and
every 30 minutes during a session, a user service compares the two for each user with `selfManagedHome`, reading the
active generation in the Nix store rather than the home directory. A mismatch
logs a line to the user journal and raises a critical notification telling them
to run `home-manager switch --flake /etc/nixos --impure`. It goes through the
standard `org.freedesktop.Notifications` interface, so GNOME, Hyprland shells,
mako, swaync and any other spec-compliant daemon show it. It is made persistent
by spec-level means only — no expiry, critical urgency and the `resident` hint —
and a repeat check replaces the existing notification instead of stacking a
second one. Whether a dismissed notification is kept in a history panel is the
daemon's choice.
Organisation-managed homes are skipped, since the rebuild already activated
them. This is a nudge, not enforcement: a user with sudo can mask the unit. It only
runs on hosts with `conf.desktop.enable`, in graphical sessions; a headless
host or an SSH-only session gets no check and no notification.

**Revoking `selfManagedHome` is destructive.** The next rebuild activates the
organisation's home, and files the user's own generation placed are removed.
Conflicting files are moved aside rather than deleted, with a suffix naming the
flake revision that displaced them.

**`users.manageHomes = false` means nothing manages the home**, neither the
organisation nor the user.

**On macOS, accounts are left alone by default.** `manageAccount` resolves to
false there, so nix-darwin does not touch the account and only the home is
configured. Setting it true adds the user to `users.knownUsers`, which is also
nix-darwin's delete list — removing that user from `conf.users.accounts`
afterwards deletes the account on the next activation for any uid above 501. A
`uid` is required in that case and must match the account's existing id, or
activation warns about an unexpected uid and skips the user. nix-darwin's own
documentation advises against managing the administrator account this way.

**On Linux, `manageAccount = false` is not supported** and is rejected by an
assertion.

**root is locked, and stays locked.** `users.users.root.hashedPassword` is `"!"`
and is re-applied on every activation, so a password set with `passwd` does not
survive the next rebuild. Rescue is an administrator with sudo, an earlier
generation from the boot menu (with the boot splash on, the menu is hidden:
hold Space on systemd-boot or press Esc on GRUB while booting to show it; with
`grubPasswordHash` set, GRUB asks for the password for older generations), or
installer media and `nixos-enter`. The
initrd emergency shell is already closed by NixOS's default
`boot.initrd.systemd.emergencyAccess`.

**Accounts without a password hash are created locked.** With
`initialHashedPassword` at its `null` default, an account can be reached only
through `sshKeys` or by an administrator setting a password. On a fleet host,
leaving SSH password authentication on produces a warning.

**On headless hosts only `wheel` may use the Nix daemon**
(`nix.settings.allowed-users`), so an unprivileged account with
`selfManagedHome` cannot build or run `home-switch` there unless the host
widens that setting.

**Fleet hosts have immutable accounts.** `users.mutableUsers` is false whenever
`fleet.repo.url` is set: activation rewrites `/etc/passwd` and `/etc/shadow`, so
a locally added user or a changed password does not survive the next sync.
Passwords come from the secrets repository when `conf.secrets.repo.url` is set,
otherwise from `initialHashedPassword`; an account with neither is locked.
