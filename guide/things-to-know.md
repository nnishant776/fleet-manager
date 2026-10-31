# Things to know

Behaviour that surprises people, by area.

**New files must be added to git.** Flakes only see files git knows about, so a
new `users/<name>/default.nix` or module is silently ignored until it is at least
`git add`ed. There is no error.

**A host directory name must equal both `conf.host.name` and the machine's
hostname.** An assertion enforces the first part. The reason for the second is
that `nixos-rebuild` with no arguments resolves `#$(hostname)`.

**A Linux-only toggle set on a Mac does nothing.** The `conf.*` vocabulary is
declared on both platforms; a platform implements what it can and ignores the
rest. `conf.desktop`, `conf.sharing`, `conf.networking`, `conf.flatpak` and
`conf.fleet` have no Darwin implementation today.

**Hardened defaults are defaults.** Every Linux host gets a kernel and service
posture from `modules/linux/baseline/` and the concern modules. Most are
`lib.mkDefault`, so a plain assignment in the host file overrides them; a few
are plain values, such as sshd's `PermitRootLogin = "no"` and
`security.sudo.execWheelOnly`, and changing those needs `lib.mkForce`.
The ones most likely to be noticed: `kernel.yama.ptrace_scope = 1` (a debugger
attaches to its own children; attaching to another process of yours needs
`sudo`); the systemd-boot menu does not allow editing the kernel command line
and keeps ten generations; DNS goes through `systemd-resolved` with
opportunistic DNS-over-TLS; `sshd` only admits members of `ssh-users`, which
privileged users join automatically and others join with
`groups = [ "ssh-users" ]`; `sshguard` runs whenever password authentication is
on; desktops run CUPS and Avahi for printer discovery, which opens UDP 5353 but
advertises nothing; core dumps are logged but not stored.

A Mac gets none of this yet. `modules/darwin/baseline.nix` holds what
nix-darwin can set, all as `lib.mkDefault`: the application firewall in stealth
mode, admitting signed software; no guest account; macOS updates installed
automatically; and sandboxed Nix builds. It is commented out until its
behaviour on a Mac that MDM manages is confirmed, since MDM may manage the same
settings. FileVault, Gatekeeper, SIP and the screen lock stay with MDM;
nix-darwin can set the screen lock for one user only.

**System outputs must evaluate without `--impure`.** `nix run .#check-purity`
enforces it. Reading machine state during evaluation, such as `/etc/os-release`,
describes the builder rather than the target and breaks automated rebuilds. Use
`conf.platform` instead. `home-switch` does need `--impure`, because a user's own
configuration lives outside the flake.

**Every Linux host audits.** `auditd` records commands run as root by a
logged-in person, and writes to the configuration checkout, the system
profiles, `/etc/passwd`, `shadow`, `group` and `sudoers`, `/etc/ssh` and, where Flatpak is on, `/var/lib/flatpak`.
`ausearch -k root-exec` reads the first; the journal is capped at 1 GiB and
90 days. With `conf.fleet.logs.lokiUrl` set, the journal goes to the fleet's Loki,
audit records included: `{host="laptop1", transport="audit"}` finds them there.

**Sessions lock on idle.** GNOME gets a locked dconf policy; Hyprland without a
shell and Sway get a system-level `swayidle` unit that also locks before sleep
and on `loginctl lock-session`. Shells such as DMS, Noctalia and Caelestia
handle idle themselves and are left alone. A user with sudo can mask the unit;
that is for attestation to notice, not for the unit to prevent.

**X11 sessions need `services.xserver.enable`.** The greeter is only given the X
session directory and launch wrapper when an X server is configured. None of the
environments offered here currently provides an X11 session.

**Virtual machines do not need `libvirtd`.** An unprivileged user can run
hardware-accelerated VMs through `qemu:///session`, plain `qemu`, or
`nixos-rebuild build-vm`, because `/dev/kvm` is world-accessible. Membership of
`libvirtd` allows defining a VM with host disk passthrough, which is equivalent
to reading any file on the host as root, so it is only granted to privileged
users.

**Installing from a working tree with uncommitted changes cannot pin the
checkout.** A committed flake passes its revision to the installed system,
which records it in `/etc/org/revision`, and the first-boot clone checks that
revision out. Uncommitted changes have no revision: `os-install` warns, and the
clone stays at the tip of the tracked ref, which may not match the system it
just installed.
