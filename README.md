# NixOS / nix-darwin Configuration Flake

A multi-platform configuration flake for NixOS, macOS and Home Manager
environments. Machines are declared under `hosts/`, per-user configuration under
`users/`, and behaviour is driven by the `conf.*` option tree. A machine's
[role](#roles) sets defaults that any host can override. The `conf.*` option tree is a
generalised group of simple toggles for most common config recipes that can be applied
on hosts without hardcoding. It is also toggleable which helps move more of the logic to
a common configuration module rather than being per host.

## Repository Layout

```
├── flake.nix                        # entry point; discovers hosts/
├── lib/
│   ├── apps/                        # os-install, system-switch, home-switch, check-purity, rpi-image
│   ├── mkHost.nix                   # host factory (NixOS / nix-darwin)
│   ├── buildUser.nix                # assembles a user's Home Manager modules
│   ├── mkUser.nix                   # standalone Home Manager builder
│   └── flakeLib.nix                 # helpers shared by this flake's modules
├── hosts/<hostname>/                # one directory per machine, auto-discovered
│   ├── default.nix                  # nixpkgs.hostPlatform and conf.* settings
│   ├── hardware-configuration.nix   # Linux only, from nixos-generate-config
│   └── *.nix                        # every file here is imported as a module
├── users/<username>/                # optional per-user configuration
│   └── default.nix                  # merged into that user's home, both paths
├── templates/
│   └── disko-luks.nix               # encrypted disk layout to copy into a host
└── modules/
    ├── conf/                        # the conf.* vocabulary: options/<concern>.nix, role.nix, machineType.nix
    ├── common/                      # configuration valid on both platforms
    ├── linux/                       # NixOS implementation, one folder per concern
    ├── darwin/                      # nix-darwin implementation
    └── user/                        # Home Manager baseline for managed users
```

Each concern has one option file in `modules/conf/options/` and one folder or
file of the same name under `modules/linux/`, so the word in `conf.<name>` finds
both the declaration and the implementation. A folder's `default.nix` only
imports its parts; each part sets its own options under its own `mkIf`.

## Usage

### First run

On a machine this flake does not configure yet, one command does everything:

```bash
curl -sSfL https://raw.githubusercontent.com/nnishant776/nixos-config/main/bootstrap | sh -s -- <hostname>
```

`bootstrap` installs Nix where it is missing — the one step Nix cannot do for
itself — and then hands over to the flake's own tools:

| Running on | It does |
|---|---|
| macOS | installs Lix if needed, then switches to `<hostname>` with the nix-darwin this flake pins |
| NixOS installer ISO | runs `os-install`; `<hostname>` is optional, and further arguments are passed to it |
| an installed NixOS host | runs `system-switch` for `<hostname>` (default: this machine's hostname) |
| another Linux | installs Lix and stops; only Home Manager is supported there |

`FLAKE=github:org/repo/<tag>` in front of `sh` makes it use another flake, or a
pinned tag so that machines do not follow every commit on `main`. MDM can run
the same command on a new Mac.

On a Mac, the host is named explicitly the first time, since the machine still
has its default hostname and the switch is what sets it. The accounts listed in
`conf.users.accounts` must already exist, normally created by MDM, since
nix-darwin leaves macOS accounts alone by default. If activation stops with
"Unexpected files in /etc", the Nix installer wrote a file nix-darwin does not
recognise; rename it by adding `.before-nix-darwin` and run the command again.
After the first switch, `sudo darwin-rebuild switch --flake …` is on the PATH.

Without `bootstrap`, from a checkout: on the NixOS ISO,
`nix --extra-experimental-features 'nix-command flakes' run .#os-install`; on a
Mac with Nix installed, `sudo nix run .#darwin-rebuild -- switch --flake .#<hostname>`.

### Adding a machine

Create `hosts/<hostname>/default.nix` with `nixpkgs.hostPlatform` and the
machine's `conf.*` settings. No change to `flake.nix` is needed. The directory
name must be exactly the machine's hostname.

Anything the `conf` vocabulary does not cover is written as a plain NixOS or
nix-darwin option in the same file, after the `conf` block. Every `.nix` file
in the host directory is imported, so `hardware-configuration.nix`,
`disko-config.nix` or a `network.nix` holding `networking.*` and
`systemd.network.*` settings need no import line.

### Building and switching a system

```bash
nixos-rebuild  switch --flake .#<hostname>     # Linux
darwin-rebuild switch --flake .#<hostname>     # macOS
system-switch                                  # wrapper; defaults to /etc/nixos and this host
```

On a machine whose configuration lives in `/etc/nixos`, `nixos-rebuild switch`
with no arguments works, because that is where it looks when given no flake.

### Switching a home configuration

Only users with `selfManagedHome` have a home configuration to activate. For
everyone else a system rebuild activates the home.

```bash
home-switch                                    # this user, /etc/nixos, this host
home-switch <username> <flake path> <host>     # explicit
home-manager switch --flake /etc/nixos --impure   # the bare CLI resolves <user>@<host> itself
```

### Installing a machine

```bash
nix run .#os-install                                   # pick the host and the disk from lists
nix run .#os-install -- -H my-laptop -d /dev/nvme0n1    # or give them as flags
nix run .#os-install -- --help
```

The host's disk layout is declared in `hosts/<name>/disko-config.nix`.
`os-install` only needs to know which host and which physical disk: anything
not given as a flag is chosen from a dropdown (`fzf`, fetched with the app, so
nothing has to be installed first). The disk list leaves out the installer's
own boot medium. It shows what will be erased and asks for the host name as
confirmation (`--yes` skips that), re-running itself with sudo if needed. `-d`
takes `NAME=DEVICE` when a layout declares more than one disk.

The live ISO keeps its writable store in RAM, so the install is arranged to
keep the system off it. The host's disko script partitions, formats and mounts
the disks at `/mnt`; `os-install` then turns on the swap the layout declares;
and `nixos-install` builds and downloads straight into `/mnt/nix/store`, with
its scratch space on the new disk and anything the ISO already holds copied
rather than downloaded. Only evaluating the configuration uses the installer's
RAM, and with swap on, that can page to the new disk. The system is installed
without a root password — root is locked by the configuration and
administration goes through sudo. A firmware boot entry is written when the
host sets `conf.hardware.boot.efiVariables`. A BIOS host must have the right
device in its layout, since GRUB installs to that one; `os-install` refuses a
different `-d` for it.

On first boot with network, a fleet host (one with `conf.fleet.repo.url`)
clones its configuration into `conf.fleet.localPath` at the revision it was
built from, and keeps retrying until it has a network. The sync starts from
that checkout.

For an encrypted layout, start from `templates/disko-luks.nix`: copy it to
`hosts/<name>/disko-config.nix` and set the swap size. disko asks for the
passphrase while formatting. Two things stay manual after the first boot,
because neither can happen before the machine exists, and `os-install` prints
both when they apply: enrolling the TPM as a second unlock method when
`conf.hardware.boot.tpm2Unlock` is set (`systemd-cryptenroll
--tpm2-device=auto --tpm2-pcrs=7 <luks device>`, asking for the passphrase), and
adding the machine's age key to `.sops.yaml` when it has secrets (`sudo
age-keygen -y /var/lib/sops-nix/key.txt`). Without a TPM chip the passphrase is
simply the only method; the TPM option falls through to it at boot.

**Fleet machines pull; they cannot push.** The checkout's push URL is set to an
unusable value by the first-boot clone and re-applied on every sync, so a `git push` from a
fleet machine fails. That is hygiene; the control is the credential: a public
repository needs none, and a private one gets a deploy key with read access
only, through `fleet.repo.deployKeySecret`.

### Building a Raspberry Pi image

A utility, run by hand when needed; nothing in a rebuild or the sync uses it.

```bash
nix run .#rpi-image                     # writes ./nixos-rpi5.img.zst
nix run .#rpi-image -- -o pi.img.zst
```

It builds one generic Raspberry Pi 5 SD image from the installer in
`nixos-raspberrypi`, plus a first-boot step. Build it once, flash it to as many
cards as needed, then give each card its own settings by creating files in
`firstboot/` on its FAT partition, which any computer can write:

| File | Effect on first boot |
|---|---|
| `hostname` | the Pi's hostname (default: `rpi-` and the last four characters of the board serial) |
| `authorized_keys` | SSH keys for `root` and the `nixos` user |
| `wifi` | `SSID=` and `PSK=` lines; deleted once applied |

A `README.txt` on the card repeats this. After the first boot, log in over SSH
and switch the Pi to its host configuration; from then on it updates from this
flake like any other host and the image is not needed again.

Building needs `aarch64-linux`: natively, through emulation
(`boot.binfmt.emulatedSystems = [ "aarch64-linux" ];` on a NixOS host), or with
a remote builder. The vendor kernel comes prebuilt from the nixos-raspberrypi
binary cache, which Nix only uses for trusted users, so run it with `sudo`
unless your user is in `nix.settings.trusted-users`; otherwise the kernel is
compiled, which under emulation takes hours.

### Signing commits for a fleet

A fleet host refuses to build anything it fetched unless the commit is signed by
a key in `conf.fleet.signing.allowedSignersFile`. Signing with an SSH key takes
three settings on each committing machine:

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
git config --global commit.gpgsign true
```

The signers file lists one `<principal> <key-type> <key>` per line, with the
public keys only, and is committed to the repository so every machine carries
it. Rotating a key means adding the new one, waiting for the fleet to converge,
then removing the old one. A lost or stolen key is removed the same way, in a
commit signed by a key still trusted. Do not rely on `valid-before` for that:
Git checks it against the commit's own date, which the signer chooses, so a
stolen key can still sign backdated commits; the option only suits retiring a
key on a planned date.

### Checking the flake

```bash
nix run .#check-purity                         # every system output must evaluate without --impure
nix eval .#nixosConfigurations.<host>.config.conf.hardware.graphics.vendor
```

## Configuration

### `conf.role`, `conf.machineType`, `conf.platform`

| Option | Type | Default | Description |
|---|---|---|---|
| `role` | enum: `minimal`/`server`/`workstation`/`developer`/`gaming`/`embedded` | `"minimal"` | What the machine is for. See [Roles](#roles) |
| `machineType` | enum: `laptop`/`desktop`/`headless`/`vm` | `headless` for server, minimal and embedded, else `desktop` | What the machine physically is. See [Machine types](#machine-types). Deduced from the role; overridable |
| `platform` | enum: `nixos`/`darwin`/`system-manager` | *(set by mkHost)* | Read-only. A host that sets it fails evaluation |

### `conf.host`

| Option | Type | Default | Description |
|---|---|---|---|
| `host.name` | str | `"localhost"` | Hostname. Must equal the host directory name |
| `host.timezone` | str | `"Asia/Kolkata"` | Timezone |
| `host.locale` | str | `"en_IN"` | Locale; also sets the `LC_*` variables |

### `conf.users`

| Option | Type | Default | Description |
|---|---|---|---|
| `users.manageHomes` | bool | `true` | Whether any home on this machine is managed, by the system or its user |
| `users.accounts` | attrsOf account | `{}` | Interactive accounts, keyed by username |

Each entry in `conf.users.accounts` takes these fields:

| Field | Type | Default | Description |
|---|---|---|---|
| `fullName` | str | *the username* | Display name |
| `email` | str | `""` | Email address |
| `privileged` | bool | `false` | Grants sudo through `wheel`, plus the privilege-adjacent groups for whichever services the host enables |
| `groups` | listOf str | `[]` | Extra groups. Privilege-granting groups are rejected here |
| `initialHashedPassword` | nullOr str | `null` | Password hash applied when the account is created, `mkpasswd -m yescrypt` format. `null` creates it locked |
| `sshKeys` | listOf str | `[]` | SSH public keys accepted for this account |
| `selfManagedHome` | bool | `false` | This user manages their own home instead of the system doing it |
| `manageAccount` | nullOr bool | `null` | Whether the account itself is created here. `null` means true on Linux and false on macOS |
| `uid` | nullOr int | `null` | Numeric user id. Required on macOS when `manageAccount` is true |
| `gid` | nullOr int | `null` | Numeric primary group id. Defaults to `20` (`staff`) on macOS |
| `homeConfig` | deferredModule | `{}` | Additional Home Manager configuration for this user |

A user's home configuration is assembled from the baseline in `modules/user/`,
anything in `users/<username>/default.nix`, their `homeConfig`, and — only for a
user with `selfManagedHome` — their own `~/.config/home-manager/home.nix`.

### `conf.hardware`

| Option | Type | Default | Description |
|---|---|---|---|
| `hardware.graphics.enable` | toggle | `false` | Hardware acceleration |
| `hardware.graphics.vendor` | enum: `intel`/`amd`/`nvidia` | `"intel"` | Driver selection |
| `hardware.bluetooth.enable` | toggle | `false` | Bluetooth stack (bluez, blueman); also enables all firmware |
| `hardware.powerManagement.enable` | toggle | `false` | Power management daemons (tuned, upower) |
| `hardware.boot.mode` | enum: `bios`/`uefi` | `"uefi"` | Firmware interface |
| `hardware.boot.loader` | nullOr enum: `systemd-boot`/`grub`/`uboot` | `"systemd-boot"` | Bootloader; GRUB when `mode` is `bios` |
| `hardware.boot.efiVariables` | toggle | `false` | Let the bootloader write EFI variables |
| `hardware.boot.tpm2Unlock` | toggle | `false` | Unlock LUKS volumes with the TPM at boot; the passphrase stays as fallback. Enrolled by `os-install` |
| `hardware.boot.grubPasswordHash` | nullOr str | `null` | GRUB only: superuser hash from `grub-mkpasswd-pbkdf2`. GRUB cannot turn its menu editor off, only lock it; with a hash the current system still boots freely, while editing entries, the GRUB console and older generations need the password. A GRUB host without one gets a warning |
| `hardware.boot.splash.enable` | toggle | `false` | Silent boot with a Plymouth splash, including the LUKS passphrase prompt on an encrypted disk. The GPU driver (Intel, AMD) loads in the initrd so the vendor logo is not wiped mid-boot, and udev/systemd status text is suppressed. The generation menu is hidden: press Space during startup on systemd-boot, or Shift/Esc on GRUB, to show it; set `boot.loader.timeout` to keep it visible |
| `hardware.boot.splash.theme` | str | `"bgrt"` | Plymouth theme; `bgrt` shows the firmware vendor logo |
| `hardware.boot.splash.themePackages` | listOf package | `[]` | Packages providing `theme`; the themes bundled with Plymouth need none |

`hardware.graphics` also takes `extraPackages` and `nix-ldLibraries`.

### `conf.networking` and `conf.sharing`

| Option | Type | Default | Description |
|---|---|---|---|
| `networking.enable` | toggle | `false` | NetworkManager |
| `networking.wifi.enable` | toggle | `false` | Wi-Fi backend |
| `networking.firewall.enable` | nullOr bool | `null` | `null` keeps the NixOS firewall at its default, which is on; `true`/`false` set it explicitly. `false` is rejected on a fleet host |
| `networking.firewall.config` | attrs | `{}` | Merged into `networking.firewall` |
| `sharing.enable` | toggle | `false` | Services that expose this machine to the network |
| `sharing.ssh.enable` | toggle | `false` | SSH server |
| `sharing.ssh.config` | attrs | `{}` | Merged into `services.openssh` |
| `sharing.rdp.enable` | toggle | `false` | Host a remote desktop (GNOME RDP). Off even with GNOME; connecting to other machines needs nothing here |

Topology — addresses, routes, VLANs, bridges, a DHCP server — is not wrapped.
Write it as `networking.*` or `systemd.network.*` options in the host directory,
for example in a `network.nix`.

### `conf.containers` and `conf.virtualisation`

| Option | Type | Default | Description |
|---|---|---|---|
| `containers.enable` | toggle | `false` | Docker (rootful and rootless) and Podman |
| `containers.extraPackages` | listOf package | `[]` | Extra container tooling |
| `containers.registries` | listOf str | `docker.io`, `quay.io`, `ghcr.io`, `registry.k8s.io` | Registries images may come from; Podman, skopeo and buildah reject any other source. Docker has no equivalent control |
| `virtualisation.enable` | toggle | `false` | KVM, QEMU, libvirt, virt-manager |
| `virtualisation.extraPackages` | listOf package | `[]` | Extra virtualisation packages |

`virtualisation` also takes `nix-ldLibraries`.

### `conf.desktop`

| Option | Type | Default | Description |
|---|---|---|---|
| `desktop.enable` | toggle | `false` | GUI stack and greeter |
| `desktop.environments.gnome.enable` | toggle | `false` | Make GNOME available |
| `desktop.environments.hyprland.enable` | toggle | `false` | Make Hyprland available |
| `desktop.environments.hyprland.shell` | enum: `none`/`caelestia`/`noctalia`/`dms` | `"none"` | Desktop shell on Hyprland |
| `desktop.environments.sway.enable` | toggle | `false` | Make Sway available |
| `desktop.greeter.wallpaper` | nullOr path | `null` | Background image for the login screen |
| `desktop.packages` | listOf package | `[]` | **Replaces** the default desktop application set |
| `desktop.extraPackages` | listOf package | `[]` | **Appends** to the desktop application set |
| `desktop.fonts.packages` | listOf package | `[]` | **Replaces** the curated font set |
| `desktop.fonts.extraPackages` | listOf package | `[]` | **Appends** to the font set |
| `desktop.multimedia.enable` | toggle | `false` | Audio and video stack |
| `desktop.printing.enable` | bool | `true` | Print to network printers: CUPS on localhost, driverless discovery, Avahi discover-only |
| `desktop.idleLockSeconds` | int | `600` | Idle time before the session locks. dconf policy on GNOME; a system-level `swayidle` unit on bare Hyprland and Sway. `0` disables it |

`desktop.multimedia` also takes `extraPackages` and `nix-ldLibraries`.

Enabling several environments makes them all available as session choices at the
greeter. Enabling the desktop also implies multimedia, graphics, power
management and flatpak, weakly, so a host can still turn any of them off.

The greeter is ReGreet running under `cage`, configured for every host with a
desktop (`modules/linux/desktop/greeter/`). It lists whichever sessions the
host installs. `desktop.greeter.wallpaper` sets its background, scaled to
cover the screen; the file is copied into the store, so a few megabytes in the
host directory is the cost. Everything else goes through the upstream options —
`programs.regreet.settings`, `extraCss`, `cageArgs`, and the theme, icon theme,
cursor theme and font settings. ReGreet does not display user avatars: it reads
only names and shells from AccountsService and has no icon widget, so avatars a
user sets in GNOME appear on GNOME's lock screen and user switcher, not at
login. The greeter's own output goes to the journal rather than the console,
so cage's start-up messages never flash on screen; read them with
`journalctl -t greeter`.

### `conf.development`

| Option | Type | Default | Description |
|---|---|---|---|
| `development.enable` | toggle | `false` | Master switch for the development stack |
| `development.extraPackages` | listOf package | `[]` | Always-installed extras |
| `development.nixLd.enable` | toggle | `false` | Export the curated library set to nix-ld, for running downloaded binaries |
| `development.nixLd.libraries` | listOf package | `[]` | **Replaces** the curated set |
| `development.nixLd.extraLibraries` | listOf package | `[]` | **Appends** to the curated set |

Each SDK (`sdk.base`, `sdk.cpp`, `sdk.go`, `sdk.rust`, `sdk.python`, `sdk.java`,
`sdk.nix`, `sdk.cue`, `sdk.lua`) and each tool (`tools.gemini`,
`tools.opencode`, `tools.rtk`) shares one shape:

| Field | Type | Default | Description |
|---|---|---|---|
| `enable` | toggle | `false` | Install this group |
| `packages` | listOf package | `[]` | **Replaces** the curated default set |
| `extraPackages` | listOf package | `[]` | **Appends** to the default set |
| `nix-ldLibraries` | listOf package | `[]` | Runtime libraries exported to nix-ld |

Editors take the same fields. `editors.neovim` and `editors.emacs` additionally
accept `configPath` for a local path and `configRepo` for a remote one.

### `conf.flatpak` and `conf.homebrew`

| Option | Type | Default | Description |
|---|---|---|---|
| `flatpak.enable` | toggle | `false` | Flatpak |
| `homebrew.enable` | toggle (macOS) | `false` | Homebrew integration |
| `homebrew.brews` / `.casks` | listOf str | `[]` | Formulae and casks |
| `homebrew.masApps` | attrsOf int | `{}` | Mac App Store applications, name to id |
| `homebrew.onActivation.cleanup` | enum: `none`/`uninstall`/`zap` | `"none"` | Cleanup mode |

### `conf.fleet`

| Option | Type | Default | Description |
|---|---|---|---|
| `fleet.repo.url` | nullOr str | `null` | Git remote the machine is deployed from |
| `fleet.repo.ref` | str | `"main"` | Branch or tag to track |
| `fleet.repo.deployKeySecret` | nullOr str | `null` | Key in `conf.secrets.file` with a read-only SSH deploy key for a private remote |
| `fleet.localPath` | str | `"/etc/nixos"` | Where the configuration checkout lives |
| `fleet.signing.allowedSignersFile` | nullOr path | `null` | SSH allowed-signers file every fetched commit must be signed by. Required when `repo.url` is set |

### `conf.secrets`

| Option | Type | Default | Description |
|---|---|---|---|
| `secrets.file` | nullOr path | `null` | sops-encrypted file with this host's secrets. `null` keeps the sops machinery off |

Each machine holds its own age key at `/var/lib/sops-nix/key.txt`; `os-install`
generates it and prints the public half, and a machine installed another way
generates one on first activation (`age-keygen -y /var/lib/sops-nix/key.txt`
prints it afterwards). Public keys go into `.sops.yaml`; secrets are encrypted
with `sops` and committed — only ciphertext ever enters the repository.

Two kinds of secret file exist:

- **Per user: `users/<name>/secrets.yaml`**, with a `password` key holding the
  account's `mkpasswd -m yescrypt` hash. Its presence is enough: the hash is
  applied on every activation to every host that declares the account, which is
  also how a password is rotated. Encrypt it to every such host plus an
  administrator key, and run `sops updatekeys` when a host is added.
- **Per host: `conf.secrets.file`**, for things that belong to the machine —
  a read-only deploy key (`fleet.repo.deployKeySecret`), certificates.

```yaml
# .sops.yaml
keys:
  - &admin age1...
  - &laptop1 age1...
  - &server1 age1...
creation_rules:
  - path_regex: users/alice/secrets\.yaml$
    key_groups: [{ age: [*admin, *laptop1, *server1] }]
  - path_regex: hosts/server1/secrets\.yaml$
    key_groups: [{ age: [*admin, *server1] }]
```

Anyone with root on a machine can read the secrets encrypted to it — which is
no more than root can read from `/etc/shadow` already.
| `fleet.autoUpdate.enable` | nullOr bool | `null` | Periodic sync and rebuild. `null` means on when `repo.url` is set |
| `fleet.autoUpdate.dates` | str | `"04:00"` | systemd `OnCalendar` expression |
| `fleet.autoUpdate.randomizedDelaySec` | int | `1800` | Jitter, so a fleet does not sync simultaneously |
| `fleet.autoUpdate.allowReboot` | bool | `false` | Reboot when the kernel or initrd changed |
| `fleet.autoUpdate.resetLocalChanges` | bool | `true` | Discard local edits in the checkout before building |
| `fleet.autoUpdate.rollbackOnFailure` | bool | `true` | Return to the previous generation if activation fails |

Point a canary group of machines at one `ref` and the rest at another, then
promote a release by moving the slower ref.

## Roles

A role is what the machine is for. Every value it sets uses `lib.mkDefault`, so
a plain assignment in a host configuration always wins without any special
syntax. Roles inherit from one another; `developer` and `gaming` build on
`workstation`.

| Role | Enables |
|---|---|
| `minimal` | networking |
| `server` | networking, containers, virtualisation, development with `sdk.base` |
| `workstation` | networking, graphics, desktop with multimedia, flatpak |
| `developer` | the workstation set, plus containers, virtualisation and development with `sdk.base`, `sdk.cpp` and neovim |
| `gaming` | the workstation set |
| `embedded` | networking, development with `sdk.base` and `sdk.cpp` |

### Machine types

A machine type is what the machine physically is, orthogonal to its role. The
defaults it sets are `lib.mkDefault` too.

| Type | Sets |
|---|---|
| `laptop` | Wi-Fi, Bluetooth, power management, thermald on Intel, sensors; hibernation stays available |
| `desktop` | power management, firmware updates |
| `headless` | `protectKernelImage` (no kexec, no hibernation), no removable-media automount, `nix.settings.allowed-users = [ "@wheel" ]`, firewall logs refused connections |
| `vm` | `protectKernelImage`, QEMU guest agent, SPICE agent when a desktop is on |

```nix
# hosts/my-laptop/default.nix
{ pkgs, ... }: {
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "developer";
    machineType = "laptop";

    host.name = "my-laptop";

    users.accounts.alice = {
      fullName = "Alice";
      privileged = true;
      selfManagedHome = true;
    };

    containers.enable = false;              # the role enables it
    hardware.graphics.vendor = "nvidia";

    desktop = {
      environments.hyprland = {
        enable = true;
        shell = "caelestia";
      };
      extraPackages = with pkgs; [ discord spotify ];
    };

    development.sdk.go.enable = true;
  };

  # Anything conf does not cover is a plain option, here.
  services.cloudflare-warp.enable = true;
}
```

### Overriding values

| You want | Use |
|---|---|
| Override a value a module set with `lib.mkDefault`, including every role default | a plain assignment |
| Override a value a module set with a plain assignment | `lib.mkForce`; two plain definitions are a conflict, not a win |
| Beat another module's `lib.mkForce` | `lib.mkOverride 40`; two `mkForce` definitions conflict |
| Append to a list | `extraPackages`, or a plain list assignment, since list definitions concatenate |

## Caveats

**New files must be added to git.** Flakes only see files git knows about, so a
new `users/<name>/default.nix` or module is silently ignored until it is at least
`git add`ed. There is no error.

**A host directory name must equal both `conf.host.name` and the machine's
hostname.** An assertion enforces the first part. The reason for the second is
that `nixos-rebuild` with no arguments resolves `#$(hostname)`.

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
them. This is a nudge, not enforcement: a user with sudo can mask the unit.

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

**A Linux-only toggle set on a Mac does nothing.** The `conf.*` vocabulary is
declared on both platforms; a platform implements what it can and ignores the
rest. `conf.desktop`, `conf.sharing`, `conf.networking`, `conf.flatpak` and
`conf.fleet` have no Darwin implementation today.

**Hardened defaults are defaults.** Every Linux host gets a kernel and service
posture from `modules/linux/baseline/` and the concern modules, all
`lib.mkDefault`, so a plain assignment in the host file overrides any of it.
The ones most likely to be noticed: `kernel.yama.ptrace_scope = 1` (a debugger
attaches to its own children; attaching to another process of yours needs
`sudo`); the systemd-boot menu does not allow editing the kernel command line
and keeps ten generations; DNS goes through `systemd-resolved` with
opportunistic DNS-over-TLS; `sshd` only admits members of `ssh-users`, which
privileged users join automatically and others join with
`groups = [ "ssh-users" ]`; `sshguard` runs whenever password authentication is
on; desktops run CUPS and Avahi for printer discovery, which opens UDP 5353 but
advertises nothing; core dumps are logged but not stored.

**System outputs must evaluate without `--impure`.** `nix run .#check-purity`
enforces it. Reading machine state during evaluation, such as `/etc/os-release`,
describes the builder rather than the target and breaks automated rebuilds. Use
`conf.platform` instead. `home-switch` does need `--impure`, because a user's own
configuration lives outside the flake.

**root is locked, and stays locked.** `users.users.root.hashedPassword` is `"!"`
and is re-applied on every activation, so a password set with `passwd` does not
survive the next rebuild. Rescue is an administrator with sudo, an earlier
generation from the boot menu, or installer media and `nixos-enter`. The
initrd emergency shell is already closed by NixOS's default
`boot.initrd.systemd.emergencyAccess`.

**Accounts without a password hash are created locked.** With
`initialHashedPassword` at its `null` default, an account can be reached only
through `sshKeys` or by an administrator setting a password. On a fleet host,
leaving SSH password authentication on produces a warning.

**The sync refuses unsigned configuration, plain `http://` remotes, and a
checkout it does not own.** `conf.fleet.signing.allowedSignersFile` is required
alongside `repo.url`; `http://` fails evaluation; and the sync exits before
building if `localPath` is not root-owned or is group- or world-writable. An
`ssh://` remote additionally needs the Git host in `programs.ssh.knownHosts`,
since root has no `known_hosts` of its own.

**Fleet hosts have immutable accounts.** `users.mutableUsers` is false whenever
`fleet.repo.url` is set: activation rewrites `/etc/passwd` and `/etc/shadow`, so
a locally added user or a changed password does not survive the next sync.
Passwords then come from `passwordSecret`; an account without one is locked.

**Every Linux host audits.** `auditd` records commands run as root by a
logged-in person, and writes to the configuration checkout, the system
profiles, identity files, `/etc/ssh` and, where Flatpak is on, `/var/lib/flatpak`.
`ausearch -k root-exec` reads the first; the journal is capped at 1 GiB and
90 days. Log shipping is not configured.

**Sessions lock on idle.** GNOME gets a locked dconf policy; Hyprland without a
shell and Sway get a system-level `swayidle` unit that also locks before sleep
and on `loginctl lock-session`. Shells such as DMS, Noctalia and Caelestia
handle idle themselves and are left alone. A user with sudo can mask the unit;
that is for attestation to notice, not for the unit to prevent.

**Automatic updates discard local edits.** With `resetLocalChanges` on, which is
the default, the checkout is hard reset to the tracked ref before building, so
anything edited in place on the machine is lost. With it off, a machine that has
diverged stops converging instead.

**A rolled-back machine does not match its revision.** If activation fails and
`rollbackOnFailure` is on, the machine returns to its previous generation while
the checkout stays at the new one. The two disagree until upstream is fixed,
which is why the condition needs reporting rather than silence.

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
checkout.** `os-install` warns and leaves `/etc/nixos` at the tip of the tracked
ref, which may not match the system it just installed.

## Inputs

| Input | Ref | Purpose |
|---|---|---|
| `nixpkgs` | nixos-26.05 | base packages and modules |
| `nixpkgs-unstable` | nixos-unstable | occasional newer packages |
| `nix-darwin` | nix-darwin-26.05 | macOS support |
| `home-manager` | release-26.05 | per-user environments |
| `dms` | stable | DankMaterialShell |
| `noctalia` | main | Noctalia shell |
| `caelestia-shell` | main | Caelestia shell |
| `disko` | master | declarative disk partitioning |
| `apple-fonts` | main | SF Pro and SF Mono |
