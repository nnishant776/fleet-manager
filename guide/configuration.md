# Configuration reference

Every `conf.*` option, by concern. The longer explanations are on their own pages: [accounts and homes](accounts.md), [networking](networking.md), [secrets](secrets.md) and [roles](roles.md).

## `conf.role`, `conf.machineType`, `conf.platform`

| Option | Type | Default | Description |
|---|---|---|---|
| `role` | enum: `minimal`/`server`/`workstation`/`developer`/`gaming`/`embedded` | `"minimal"` | What the machine is for. See [Roles](roles.md) |
| `machineType` | enum: `laptop`/`desktop`/`headless`/`vm` | `headless` for server, minimal and embedded, else `desktop` | What the machine physically is. See [Machine types](roles.md#machine-types). Deduced from the role; overridable |
| `platform` | enum: `nixos`/`darwin`/`system-manager` | *(set by mkHost)* | Read-only. A host that sets it fails evaluation |

## `conf.host`

| Option | Type | Default | Description |
|---|---|---|---|
| `host.name` | str | `"localhost"` | Hostname. Must equal the host directory name |
| `host.timezone` | str | `"Asia/Kolkata"` | Timezone. NixOS only |
| `host.locale` | str | `"en_IN"` | Locale; also sets the `LC_*` variables. NixOS only |

## `conf.users`

| Option | Type | Default | Description |
|---|---|---|---|
| `users.manageHomes` | bool | `true` | Whether any home on this machine is managed, by the system or its user |
| `users.accounts` | attrsOf account | `{}` | Interactive accounts, keyed by username |

Each entry in `conf.users.accounts` takes these fields:

| Field | Type | Default | Description |
|---|---|---|---|
| `fullName` | str | *the username* | Display name |
| `email` | str | `""` | Email address |
| `privileged` | bool | `false` | Grants sudo through `wheel`, plus the privilege-adjacent groups for whichever services the host enables. NixOS only |
| `groups` | listOf str | `[]` | Extra groups. Privilege-granting groups are rejected here. NixOS only. With SSH sharing on, an unprivileged account needs `ssh-users` here to log in over SSH |
| `initialHashedPassword` | nullOr str | `null` | Password hash applied when the account is created, `mkpasswd -m yescrypt` format. `null` creates it locked. On a host with `conf.secrets.repo.url` the hash comes from the secrets repository instead. NixOS only |
| `sshKeys` | listOf str | `[]` | SSH public keys accepted for this account. On a host with `conf.secrets.repo.url`, listing any here is an error: the keys go in the account's file (`secrets.authorizedKeys`). NixOS only |
| `sudoSshKeys` | listOf str | `[]` | SSH public keys that unlock sudo for this privileged account instead of its password. Not the same as `sshKeys`. Listing keys on an unprivileged account is an error. On a host with `conf.secrets.repo.url`, listing any here is an error: the keys go in the account's file (`secrets.sudoKeys`). NixOS only. See [Sudo with an SSH key](accounts.md#sudo-with-an-ssh-key) |
| `secrets.password` | bool | `true` | Take the account's password hash from `users/<name>/<host>.yaml` in the secrets repository. Ignored on a host without `conf.secrets.repo.url`; setting `initialHashedPassword` as well is an error on an opted-in host |
| `secrets.sshKey` | bool | `false` | Deploy a per-host SSH key from the same file to `/run/secrets/users/<name>/ssh_key`. Can be turned on any time after the account exists. Ignored on a host without `conf.secrets.repo.url`. NixOS only. Also deploys `ssh_key.pub` (mode 0400, as all of a user's secrets), and the key becomes the user's default SSH identity and git signing key |
| `secrets.authorizedKeys` | bool | `false` | Accept the SSH public keys in `users/<name>/<host>.yaml` (`authorized_keys`, one per line) for this account's login, instead of `sshKeys`, which a host with secrets may not use. sshd reads them from `/run/secrets/users/<name>/authorized_keys` at each login, so changing them needs no rebuild. Ignored on a host without `conf.secrets.repo.url`. NixOS only. See [Secrets](secrets.md) |
| `secrets.sudoKeys` | bool | `false` | Accept the SSH public keys in the same file (`sudo_keys`, one per line) to unlock sudo for this privileged account, besides `sudoSshKeys`. On an unprivileged account it is an error. Ignored on a host without `conf.secrets.repo.url`. NixOS only. See [Sudo with an SSH key](accounts.md#sudo-with-an-ssh-key) |
| `secrets.own` | bool | `false` | Decrypt the account's whole file, `users/<name>/<host>.yaml`, to `/run/secrets/users/<name>/all`, readable by the account alone (mode 0400), and install `fleet-user-secret`, which prints one of the secrets the user keeps in it (`secrets.<name>`, stored with `fleet user secret set`). For a user with a branch of their own. Ignored on a host without `conf.secrets.repo.url`. NixOS only. See [Secrets](secrets.md) |
| `selfManagedHome` | bool | `false` | This user manages their own home instead of the system doing it |
| `manageAccount` | nullOr bool | `null` | Whether the account itself is created here. `null` means true on Linux and false on macOS. `false` is an error on Linux. On macOS, `true` puts the account on nix-darwin's delete list, so removing it from `accounts` deletes the macOS account at the next switch |
| `uid` | nullOr int | `null` | Numeric user id. Required on macOS when `manageAccount` is true |
| `gid` | nullOr int | `null` | Numeric primary group id. Defaults to `20` (`staff`) on macOS |
| `homeConfig` | deferredModule | `{}` | Additional Home Manager configuration for this user |

How sudo with an SSH key works, and how a home is assembled, is in [Accounts and homes](accounts.md).

## `conf.hardware`

| Option | Type | Default | Description |
|---|---|---|---|
| `hardware.graphics.enable` | toggle | `false` | Hardware acceleration |
| `hardware.graphics.vendor` | enum: `intel`/`amd`/`nvidia` | `"intel"` | Driver selection |
| `hardware.powerManagement.enable` | toggle | `false` | Power management daemons (tuned, upower) |
| `hardware.disk.enable` | bool | `false` | Generate `disko.devices` from the options below. `false` means the host provides its own `disko-config.nix`, or is not installable with `os-install` |
| `hardware.disk.device` | nullOr str | `null` | Primary disk: boot partitions and the first physical volume. Required when enabled. `os-install -d main=DEV` overrides it |
| `hardware.disk.encrypt` | bool | `true` | Wrap every physical volume in a LUKS2 container (`allowDiscards`). The only encryption switch; `false` for VMs. A generated unencrypted layout on a laptop gives a warning |
| `hardware.disk.espSize` | str | `"1G"` | Size of the EFI system partition |
| `hardware.disk.volumes.<name>` | attrsOf volume | `fleet`, `swap`, `root` | Logical volumes in the volume group `main`. Keys merge, so a host adds volumes without restating defaults, and drops one with `enable = false`. See below |
| `hardware.disk.extraDevices.<name>` | attrsOf str | `{}` | Further whole disks that join the volume group, keyed by a short name (`data = "/dev/sdb"`). `os-install -d <name>=DEV` overrides each |
| `hardware.boot.mode` | enum: `bios`/`uefi` | `"uefi"` | Firmware interface |
| `hardware.boot.loader` | nullOr enum: `systemd-boot`/`grub`/`uboot` | `"systemd-boot"` | Bootloader; GRUB when `mode` is `bios`. `uboot` writes `/boot/extlinux/extlinux.conf` for the U-Boot already on the board, which this flake does not install. `null` turns no loader on, for a board module that brings its own, such as the Raspberry Pi's |
| `hardware.boot.efiVariables` | toggle | `false` | Let the bootloader write EFI variables |
| `hardware.boot.tpm2Unlock` | toggle | `false` | Unlock LUKS volumes with the TPM at boot; the passphrase stays as fallback. Enrolled by `os-install` |
| `hardware.boot.grubPasswordHash` | nullOr str | `null` | GRUB only: superuser hash from `grub-mkpasswd-pbkdf2`. GRUB cannot turn its menu editor off, only lock it; with a hash the current system still boots freely, while editing entries, the GRUB console and older generations need the password. A GRUB host without one gets a warning |
| `hardware.boot.splash.enable` | toggle | `false` | Silent boot with a Plymouth splash, including the LUKS passphrase prompt on an encrypted disk. The GPU driver (Intel, AMD) loads in the initrd so the vendor logo is not wiped mid-boot, and udev/systemd status text is suppressed. The generation menu is hidden: press Space during startup on systemd-boot, or Shift/Esc on GRUB, to show it; set `boot.loader.timeout` to keep it visible |
| `hardware.boot.splash.theme` | str | `"bgrt"` | Plymouth theme; `bgrt` shows the firmware vendor logo |
| `hardware.boot.splash.themePackages` | listOf package | `[]` | Packages providing `theme`; the themes bundled with Plymouth need none |

`hardware.graphics` also takes `extraPackages` and `nix-ldLibraries`.

Each entry in `conf.hardware.disk.volumes` takes these fields:

| Field | Type | Default | Description |
|---|---|---|---|
| `enable` | bool | `true` | `false` removes a default volume, for example `swap` on a VM |
| `type` | enum: `filesystem`/`swap` | `"filesystem"` | What the volume holds |
| `size` | str | *(none)* | LVM syntax: `"16G"`, `"20%VG"`, `"100%FREE"`. At most one volume may use `100%FREE` |
| `mountpoint` | nullOr str | `null` | Where it is mounted. Required for filesystems |
| `fsType` | str | `"ext4"` | Any filesystem disko supports (`ext4`, `xfs`, `btrfs`, …) |
| `mountOptions` | listOf str | `[ "defaults" ]` | Mount options |
| `neededForBoot` | bool | `false` | Mount the volume in the initrd |
| `priority` | int | `1000` | Creation order in the volume group, lower first. A `100%FREE` volume left at the default is created last |

The module declares `fleet` (`64M`, `/var/lib/fleet`, `neededForBoot`; it must
stay enabled), `swap` (`16G`) and `root` (`100%FREE`, `/`), each value as a
default.

## `conf.networking` and `conf.sharing`

| Option | Type | Default | Description |
|---|---|---|---|
| `networking.enable` | toggle | `false` | Networking, managed by `networking.backend` |
| `networking.backend` | enum: `networkmanager`/`networkd` | `"networkmanager"` | `networkmanager` is for machines where users pick networks. `networkd` is for appliances: DHCP on every wired and Wi-Fi interface, Wi-Fi through wpa_supplicant, and only privileged users can change networks |
| `networking.macNames` | bool | `false` | Names every physical network interface, Wi-Fi and wired, after its bus and MAC (e.g. `wlu001a2b3c4d5e`), from the next boot |
| `networking.wireless.enable` | bool | `true` | Master switch for every radio: Wi-Fi, the hotspot and Bluetooth. Set it to `false` to turn them all off at once. Wired networking keeps working |
| `networking.wireless.countryCode` | nullOr str | `null` | Two capital letters, such as `DE`: the country whose radio rules apply. Without it the strict worldwide rules apply. Used by `networkd` |
| `networking.wireless.wifi.enable` | toggle | `false` | Wi-Fi client. Needs `networking.enable`; off when `networking.wireless.enable` is off; on by default with `machineType = "laptop"` |
| `networking.wireless.wifi.interfaces` | attrsOf submodule | `{}` | With `networkd`: the client's interfaces, by name. Each joins its own networks, kept in `/etc/wpa_supplicant/<name>.conf`. `{}` uses every Wi-Fi interface, sharing `imperative.conf`. When a hotspot is declared and the Wi-Fi client is on (`wifi.enable`), it must name them, so the client never takes a hotspot's adapter |
| `networking.wireless.wifi.interfaces.<name>.routeMetric` | nullOr ints.unsigned | `null` | Which network carries internet traffic when several give a default route. The lowest wins. `null` is networkd's 1024, as for a wired network |
| `networking.wireless.hotspots` | attrsOf submodule | `{}` | Wi-Fi hotspots that share this machine's connection with other devices, keyed by the adapter's interface name. Each has a service of its own. Needs `networking.backend = "networkd"`, `networking.wireless.countryCode`, the client's interfaces named in `networking.wireless.wifi.interfaces` when the Wi-Fi client is on, and each password file. Off when `networking.wireless.enable` is off |
| `networking.wireless.hotspots.<name>.enable` | bool | `false` | Turns this hotspot on. Off, its adapter is still kept from the Wi-Fi client, so turning it on changes nothing else |
| `networking.wireless.hotspots.<name>.ssid` | str | the host name | Network name the hotspot shows (1 to 32 bytes). Each hotspot needs its own |
| `networking.wireless.hotspots.<name>.band` | enum: `2g`/`5g`/`6g` | `"5g"` | Frequency band. `6g` is Wi-Fi 6E and needs an adapter and a country that allow it (India allows indoor use since 2026) |
| `networking.wireless.hotspots.<name>.channel` | nullOr ints.positive | `null` | Channel. `null` picks 6, 36 or 37 for `2g`, `5g`, `6g` |
| `networking.wireless.hotspots.<name>.width` | `"max"` or 20/40/80/160/320 | `"max"` | Channel width in MHz. `"max"` takes the widest the adapter and the country allow, and stays at 20 on `2g`. A number pins it, and the hotspot does not start if that width is not allowed |
| `networking.wireless.hotspots.<name>.radarChannels` | bool | `false` | Lets `"max"` use channels shared with radar. The hotspot then waits a minute or more before it starts, and many USB adapters cannot do it. 160 MHz on `5g` needs them |
| `networking.wireless.hotspots.<name>.security` | enum: `wpa3`/`wpa2-wpa3` | `"wpa3"` | `wpa3` lets in WPA3 devices only. `wpa2-wpa3` also lets older devices in. 6 GHz allows WPA3 only |
| `networking.wireless.hotspots.<name>.address` | str | `"10.42.0.1/24"` | The hotspot's own address and subnet. Joining devices get an address in it and reach the internet through this machine. Two hotspots' subnets must not overlap |
| `networking.wireless.hotspots.<name>.hostAccess` | bool | `false` | Lets devices on this hotspot reach this machine's own services, such as SSH, as devices on the uplink side can. Off, they reach only its DHCP server |
| `networking.wireless.hotspots.<name>.passwordFile` | str | `"/var/lib/hotspot/<name>"` | File on the machine holding only this hotspot's password (8 to 63 characters, no `\|`), created by root with mode 0600. It is read when the hotspot starts and never copied into the Nix store |
| `networking.wireless.hotspots.<name>.settings` | attrsOf (hostapd value or list of them) | `{}` | Extra hostapd settings for this hotspot. They come last, so they win. Put width keys in `width`, not here, as `settings` skips its checks |
| `networking.wireless.hotspots.<name>.extraConfig` | lines | `""` | Lines added to this hotspot's hostapd configuration as they are, after `settings` |
| `networking.wireless.bluetooth.enable` | toggle | `false` | Bluetooth (bluez, blueman); also turns on all firmware. Needs `networking.enable`; off when `networking.wireless.enable` is off; on by default with `machineType = "laptop"` |
| `networking.firewall.enable` | nullOr bool | `null` | `null` keeps the NixOS firewall at its default, which is on; `true`/`false` set it explicitly. `false` is rejected on a fleet host |
| `networking.firewall.config` | attrs | `{}` | Merged into `networking.firewall`, for its options such as logging. `extraCommands` and `extraStopCommands` (iptables) are rejected |
| `networking.firewall.inputRules` | lines | `""` | nftables rules of this host's own for new connections to it. They add access; they cannot remove what an open port allows |
| `networking.firewall.forwardRules` | lines | `""` | nftables rules of this host's own for traffic passing through. Setting any turns on "forwarding denied unless allowed" |
| `networking.firewall.tables` | attrs of { family; content } | `{}` | Whole nftables tables of this host's own. `family` defaults to `inet`. Names NixOS, Docker, podman, libvirt or systemd-networkd already use are rejected, and so is `hotspot-guard`, this flake's own |
| `sharing.enable` | toggle | `false` | Services that expose this machine to the network. Gates `rdp`; `ssh` does not need it |
| `sharing.ssh.enable` | toggle | `false` | SSH server, keys only, for members of `ssh-users`. Needs `networking.enable`. Set by the support account |
| `sharing.ssh.config` | attrs | `{}` | Merged into `services.openssh` |
| `sharing.rdp.enable` | toggle | `false` | Host a remote desktop (GNOME RDP). Off even with GNOME; connecting to other machines needs nothing here |

Topology — addresses, routes, VLANs, bridges, a DHCP server — is not wrapped.
Write it as `networking.*` or `systemd.network.*` options in the host directory,
for example in a `network.nix`.

The firewall, interface names, Wi-Fi networks and hotspots are explained in [Networking](networking.md).

## `conf.containers` and `conf.virtualisation`

| Option | Type | Default | Description |
|---|---|---|---|
| `containers.enable` | toggle | `false` | Docker (rootful and rootless) and Podman |
| `containers.extraPackages` | listOf package | `[]` | Extra container tooling |
| `containers.registries` | listOf str | `docker.io`, `quay.io`, `ghcr.io`, `registry.k8s.io` | Registries images may come from; Podman, skopeo and buildah reject any other source. Docker has no equivalent control. The list is also the search order for image names given without a registry |
| `virtualisation.enable` | toggle | `false` | KVM, QEMU, libvirt, virt-manager |
| `virtualisation.extraPackages` | listOf package | `[]` | Extra virtualisation packages |

`virtualisation` also takes `nix-ldLibraries`.

## `conf.desktop`

| Option | Type | Default | Description |
|---|---|---|---|
| `desktop.enable` | toggle | `false` | GUI stack and greeter |
| `desktop.environments.gnome.enable` | toggle | `false` | Make GNOME available |
| `desktop.environments.hyprland.enable` | toggle | `false` | Make Hyprland available |
| `desktop.environments.hyprland.shell` | enum: `none`/`caelestia`/`noctalia`/`dms` | `"none"` | Desktop shell on Hyprland |
| `desktop.environments.sway.enable` | toggle | `false` | Make Sway available |
| `desktop.greeter.wallpaper` | nullOr path | *bundled image* | Background image for the login screen; `null` for ReGreet's plain background |
| `desktop.packages` | listOf package | `[]` | **Replaces** the default desktop application set |
| `desktop.extraPackages` | listOf package | `[]` | **Appends** to the desktop application set |
| `desktop.fonts.packages` | listOf package | `[]` | **Replaces** the curated font set |
| `desktop.fonts.extraPackages` | listOf package | `[]` | **Appends** to the font set |
| `desktop.multimedia.enable` | toggle | `false` | Audio and video stack |
| `desktop.browserPolicies` | attrsOf anything | Safe Browsing at the standard level, no card numbers in autofill | Chromium enterprise policies by name, enforced in Chromium and Brave and not changeable in their settings. A host overrides one by name; `null` unsets it |
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

## `conf.development`

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

Editors take the same fields. All three (`editors.neovim`, `editors.emacs` and
`editors.vscode`) additionally accept `configPath` and `configRepo` (str,
default `""`); no module reads them yet.

## `conf.flatpak` and `conf.homebrew`

| Option | Type | Default | Description |
|---|---|---|---|
| `flatpak.enable` | toggle | `false` | Flatpak |
| `homebrew.enable` | toggle (macOS) | `false` | Homebrew integration |
| `homebrew.brews` / `.casks` | listOf str | `[]` | Formulae and casks |
| `homebrew.masApps` | attrsOf int | `{}` | Mac App Store applications, name to id |
| `homebrew.onActivation.autoUpdate` | bool | `false` | Update Homebrew itself on activation |
| `homebrew.onActivation.cleanup` | enum: `none`/`uninstall`/`zap` | `"none"` | Cleanup mode |
| `homebrew.onActivation.upgrade` | bool | `false` | Upgrade installed packages on activation |

## `conf.fleet`

| Option | Type | Default | Description |
|---|---|---|---|
| `fleet.repo.url` | nullOr str | `null` | Git remote the machine is deployed from. An `https://` address is fetched with no key; an SSH one, for a private remote, with the host's own configuration key, made with its identity and listed in its enrollment file as `config_deploy_pubkey`, so it needs `conf.secrets.repo.url`. See [A private fleet](private-fleet.md) |
| `fleet.repo.ref` | str | `"main"` | Branch or tag to track |
| `fleet.localPath` | str | `"/etc/nixos"` | Where the configuration checkout lives. It belongs to the fleet: the first-boot clone replaces what is there without a checkout, and every sync leaves it detached at the verified commit. On NixOS every account's git trusts it (`safe.directory`) |
| `fleet.signing.allowedSignersFile` | nullOr path | `null` | SSH allowed-signers file. The newest commit of every fetch must be signed by a key in it, for the configuration and for the secrets repository. Required when `repo.url` is set |
| `fleet.support.enable` | nullOr bool | `null` | Create an account for the maintainer to reach the host with. `null` means on when `conf.secrets.repo.url` is set. NixOS only; macOS hosts get no support account yet. Sets `sharing.ssh.enable`. Every sync checks the account, its keys, its sudo rule and sshd, and repairs them if they were broken; `fleet host check` reports a sync that has stopped running |
| `fleet.support.user` | str | `"support"` | Name of the support account |
| `fleet.support.sshKeys` | listOf str | `[]` | The maintainer's public keys, ideally hardware-backed `sk-ssh-ed25519`. For a host without `conf.secrets.repo.url`; on a host with secrets it must be empty and the keys go in the support account's file (`keysFromSecrets`). Required when the account is enabled on a host without secrets. The account is privileged with a sudo rule without a password, since its password is locked and the key is the factor |
| `fleet.support.keysFromSecrets` | bool | `false` | Accept the keys in the support account's file on the host's branch (`users/support/<host>.yaml`, `authorized_keys`), so they change without a rebuild. Required on a host with secrets, where `sshKeys` has to be empty; requires `conf.secrets.repo.url`. Set them with `fleet secrets user-keys support <host> --authorized-keys FILE`, or let `fleet support-key rotate` do it |
| `fleet.support.consolePassword` | bool | `false` | Also take a password hash for the account from `hosts/<host>.yaml` (`support_password`), for console login without a network |
| `fleet.autoUpdate.enable` | nullOr bool | `null` | Periodic sync and rebuild. `null` means on when `repo.url` is set |
| `fleet.autoUpdate.dates` | str | `"04:00"` | systemd `OnCalendar` expression |
| `fleet.autoUpdate.randomizedDelaySec` | int | `1800` | Jitter, so a fleet does not sync simultaneously |
| `fleet.autoUpdate.allowReboot` | bool | `false` | Reboot when the kernel or initrd changed |
| `fleet.autoUpdate.resetLocalChanges` | bool | `true` | Discard local edits and local branches in the checkout before building |
| `fleet.logs.lokiUrl` | nullOr str | `null` | Push URL of the fleet's Loki (`http://loki.lan:3100/loki/api/v1/push`). When set, Grafana Alloy sends the journal there, audit records included, labelled `host`, `unit`, `level` and `transport` |
| `fleet.autoUpdate.rollbackOnFailure` | bool | `true` | Return to the previous generation if activation fails |
| `fleet.personal.enable` | bool | `false` | A host handed out to one person, who makes their own account at the first boot. Accounts become mutable. See [Personal machines](accounts.md#personal-machines). Needs the NetworkManager backend; NixOS only |
| `fleet.personal.ownerPrivileged` | bool | `true` | Make the owner an administrator, with root. `false` gives an ordinary account that can only join and switch networks |

Point a canary group of machines at one `ref` and the rest at another, then
promote a release by moving the slower ref.

Personal machines are explained in [Accounts and homes](accounts.md#personal-machines).

## `conf.secrets`

| Option | Type | Default | Description |
|---|---|---|---|
| `secrets.repo.url` | nullOr str | `null` | The private secrets repository, as an SSH URL (`git@github.com:example-org/secrets.git`). Requires `fleet.repo.url` and `fleet.signing.allowedSignersFile`. `null` opts the host out entirely: no secrets sync, no host identity, sops off, `accounts.<name>.secrets.*` ignored, no support account |
| `secrets.repo.ref` | str | `"host/<host name>"` | Branch of the secrets repository to track. Each host has a branch of its own, `host/<name>`, holding its files and no other host's; `fleet secrets host-enroll` creates it |
| `secrets.statePath` | str | `"/var/lib/fleet"` | Where the host's identity, enrollment file and secrets snapshot live. Expected to be its own volume |

Setting `repo.url` requires `conf.fleet.signing.allowedSignersFile`, which every
fetched commit is verified against, and `conf.fleet.repo.url`, since the fleet
sync is what refreshes the secrets. The URL must be an SSH one, because the host
authenticates with its deploy key; `http://` and `https://` are rejected.
These checks apply on NixOS; on macOS `secrets.repo.url` only sets up sops.

How the secrets work is in [Secrets](secrets.md), and what to do in [Secrets runbooks](secrets-runbooks.md).
