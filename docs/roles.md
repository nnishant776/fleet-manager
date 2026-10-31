# 4. Roles and machine types

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 3. Building a Raspberry Pi image](raspberry-pi.md) · [Contents](README.md) · [5. Accounts and homes →](accounts.md)
<!-- /book:top -->

Two options decide most of a machine's defaults: `conf.role`, what it is for,
and `conf.machineType`, what it physically is. This chapter lists what each
sets, the defaults worked out from other options, what turning an option on
brings with it, and how a host overrides any of it. Every option itself is in
the [Configuration reference](configuration.md).

<!-- book:sections -->
In this chapter:

- [Roles](#roles)
- [Machine types](#machine-types)
- [Computed defaults](#computed-defaults)
- [Cascading implications](#cascading-implications)
- [An example host](#an-example-host)
- [Overriding values](#overriding-values)
<!-- /book:sections -->

## Roles

A role is what the machine is for. Every value it sets uses `lib.mkDefault`, so
a plain assignment in a host configuration always wins without any special
syntax. Roles inherit from one another using `lib.recursiveUpdate`; `developer`
and `gaming` build on `workstation`, while `server` and `embedded` build on
`minimal`.

When `conf.machineType` is omitted, its default is deduced from `conf.role`:
roles without an interactive graphical seat (`server`, `minimal`, `embedded`)
default to `headless`, while graphical workstation roles (`workstation`,
`developer`, `gaming`) default to `desktop`.

| Role | Direct `conf.*` Defaults | Default `machineType` |
|---|---|---|
| `minimal` | `networking.enable` | `headless` |
| `server` | `networking.enable`, `containers.enable`, `virtualisation.enable`, `development.enable`, `development.sdk.base.enable` | `headless` |
| `workstation` | `networking.enable`, `hardware.graphics.enable`, `desktop.enable`, `desktop.multimedia.enable`, `flatpak.enable` | `desktop` |
| `developer` | The `workstation` set, plus `containers.enable`, `virtualisation.enable`, `development.enable`, `development.sdk.base.enable`, `development.sdk.cpp.enable`, `development.editors.neovim.enable` | `desktop` |
| `gaming` | The `workstation` set | `desktop` |
| `embedded` | `networking.enable`, `development.enable`, `development.sdk.base.enable`, `development.sdk.cpp.enable` | `headless` |

## Machine types

A machine type is what the machine physically is, orthogonal to its role. The
defaults it sets are `lib.mkDefault` too.

A machine type contributes defaults across two levels:
1. **`conf.*` options** applied with `flakeLib.mkDefaults` (`modules/conf/machineType.nix`).
2. **System, kernel, hardware and security postures** keyed directly off `conf.machineType` across the Linux modules.

| Type | `conf.*` Options | System, Hardware & Security Posture (Linux) |
|---|---|---|
| `laptop` | `networking.wireless.wifi.enable`, `networking.wireless.bluetooth.enable`, `hardware.powerManagement.enable` | `services.fwupd.enable = true`; `services.thermald.enable` on Intel (follows `hardware.cpu.intel.updateMicrocode`); `hardware.sensor.iio.enable = true`; `security.protectKernelImage = false` (kexec and hibernation stay available); a generated disk layout with `hardware.disk.encrypt = false` gives an evaluation warning |
| `desktop` | `hardware.powerManagement.enable` | `services.fwupd.enable = true`; `security.protectKernelImage = false` (kexec and hibernation stay available); Wi-Fi and Bluetooth stay off unless set |
| `headless` | *(none)* | Hardened posture: `security.protectKernelImage = true` (no kexec, no hibernation); `nix.settings.allowed-users = [ "@wheel" ]` on NixOS; `networking.firewall.logRefusedConnections = true`; `services.fwupd.enable = false` |
| `vm` | *(none)* | Guest integration: `services.qemuGuest.enable = true`; `services.spice-vdagentd.enable` follows `desktop.enable`; `security.protectKernelImage = true`; `services.fwupd.enable = false`. Power daemons still follow `desktop.enable` (see below), so a desktop-role VM gets them unless `hardware.powerManagement.enable = false` |

Only the `conf.*` column applies on macOS; the posture column is implemented
by the Linux modules.

## Computed defaults

Some options have no fixed default: when left unset (usually `null`) they
resolve from other options. A plain assignment always wins.

| Option | Unset resolves to | Where |
|---|---|---|
| `conf.machineType` | `"headless"` for the `server`, `minimal` and `embedded` roles, otherwise `"desktop"` | `modules/conf/machineType.nix` |
| `conf.hardware.boot.loader` | `"grub"` when `hardware.boot.mode = "bios"`, otherwise `"systemd-boot"` | `modules/linux/baseline/boot.nix` |
| `conf.networking.firewall.enable` | `null`: the NixOS default, which is on. `false` is rejected when `conf.fleet.repo.url` is set | `modules/linux/networking/firewall.nix` |
| `conf.fleet.autoUpdate.enable` | on when `conf.fleet.repo.url` is set, off otherwise. `true` without a `repo.url` is an error | `modules/linux/fleet/sync.nix` |
| `conf.fleet.support.enable` | on when `conf.secrets.repo.url` is set, off otherwise. NixOS only | `modules/linux/fleet/support.nix` |
| `conf.users.accounts.<name>.manageAccount` | `true` on NixOS, `false` on macOS (accounts come from MDM) | `lib/flakeLib.nix` |
| `conf.users.accounts.<name>.fullName` | the username | `modules/conf/options/users.nix` |
| `conf.desktop.greeter.wallpaper` | the bundled `wallhaven-j5oy7m.jpg`; `null` gives ReGreet's plain background | `modules/linux/desktop/greeter/wallpaper.nix` |
| `conf.hardware.disk.volumes` | `fleet` (`64M`, `/var/lib/fleet`), `swap` (`16G`), `root` (`100%FREE`, `/`), each field a default, when `hardware.disk.enable` | `modules/linux/baseline/disk.nix` |
| `users.mutableUsers` | `false` when `conf.fleet.repo.url` is set, so accounts and passwords are exactly what the configuration declares | `modules/linux/host/users.nix` |
| `programs.nix-ld.enable` | follows `conf.development.enable` | `modules/linux/development/nix-ld.nix` |
| `services.sshguard.enable` | on only while sshd accepts passwords | `modules/linux/sharing/ssh.nix` |
| `boot.loader.timeout` | `0` on systemd-boot and `1` on GRUB (menu hidden) when `hardware.boot.splash.enable` | `modules/linux/baseline/boot.nix` |

## Cascading implications

Enabling a toggle, by hand or through `role` and `machineType`, cascades into
secondary defaults, services and account groups. The roles that turn each
trigger on are in italics. "Weakly" means `lib.mkDefault`: a host can still
turn the implied option off.

| Trigger | Cascading Implications |
|---|---|
| `conf.networking.enable`<br>*(all roles)* | • NetworkManager (or systemd-networkd with `networking.backend = "networkd"`), with `systemd-resolved` doing opportunistic DNS-over-TLS and `DNSSEC = "allow-downgrade"`<br>• Wi-Fi stays off, and NetworkManager leaves Wi-Fi devices alone, unless `networking.wireless.wifi.enable` is on<br>• Prerequisite for `networking.wireless.wifi`, `networking.wireless.bluetooth` and `sharing.ssh`: without it those three do nothing<br>• Account groups: with NetworkManager, `network-users` (join and switch networks only) for unprivileged accounts and `networkmanager` for privileged ones; with networkd, `wpa_supplicant` for privileged accounts (while Wi-Fi is on) and nothing for the others |
| `conf.networking.wireless.wifi.enable`<br>*(laptop)* | • With `networking.enable` and `networking.wireless.enable`: wpa_supplicant, the Wi-Fi client (the NixOS option `networking.wireless.enable`)<br>• With named `networking.wireless.wifi.interfaces`: one wpa_supplicant per interface, each with its own file<br>• Named `wifi.interfaces` need `networking.backend = "networkd"`; otherwise a build error |
| `conf.networking.wireless.bluetooth.enable`<br>*(laptop)* | • With `networking.enable` and `networking.wireless.enable`: `hardware.bluetooth.enable`, `hardware.enableAllFirmware`, `hardware.alsa.enableBluetooth`, `services.blueman.enable` |
| `conf.networking.wireless.hotspots.<name>.enable` | • With `networking.enable`, `networking.wireless.enable` and the networkd backend: hostapd in a service of its own on that adapter, with WPA3 (on 6 GHz with protected management frames)<br>• systemd-networkd gives joining devices addresses and shares the uplink through NAT; DHCP is allowed through the firewall on that hotspot only<br>• Forwarded traffic is denied unless allowed; the hotspot reaches the uplink but not other hotspots, and reaches this machine only for DHCP unless `hostAccess`<br>• The widest channel width the adapter and the country allow, unless `width` pins it; the country is passed to the kernel at boot<br>• Build errors unless: the networkd backend, `wireless.countryCode` set, named `wifi.interfaces` when the Wi-Fi client is on, unique SSIDs, non-overlapping subnets, and WPA3 on 6 GHz |
| `conf.networking.macNames` | • A udev rule that renames physical network interfaces, at boot or when plugged in, after their bus and MAC address<br>• Needs the systemd initrd, which is on by default |
| `conf.hardware.powerManagement.enable`<br>*(laptop, desktop, or `desktop.enable`)* | • `services.upower.enable`, `services.tuned.enable` |
| `conf.desktop.enable`<br>*(workstation, developer, gaming)* | • Weakly enables `desktop.multimedia.enable`, `hardware.graphics.enable`, `hardware.powerManagement.enable` and `flatpak.enable`, whatever the role<br>• Polkit, GVFS, display manager with the ReGreet greeter, AccountsService, `udisks2` (removable-media automount)<br>• Account groups: `video` and `input` for every account<br>• On `machineType = "vm"`: `services.spice-vdagentd`<br>• CUPS on localhost, cups-browsed and Avahi (mDNS port open); off with `desktop.printing.enable = false` |
| `conf.containers.enable`<br>*(server, developer)* | • Docker (rootful daemon, plus rootless with `DOCKER_HOST` set for users) and Podman; Podman, skopeo and buildah accept only `containers.registries`<br>• Forwarded traffic is denied unless allowed; the containers' own traffic is allowed<br>• Account groups: `docker` and `podman` for privileged accounts only |
| `conf.virtualisation.enable`<br>*(server, developer)* | • `libvirtd` with swtpm, virt-manager<br>• Forwarded traffic is denied unless allowed; the VMs' own traffic is allowed<br>• Account groups: `kvm` for every account, `libvirtd` for privileged accounts only |
| `conf.development.enable`<br>*(server, developer, embedded)* | • Weakly enables `programs.nix-ld.enable`, for running unpatched foreign binaries<br>• Enabled SDK, editor, tool, graphics, multimedia and virtualisation groups export their `nix-ldLibraries`; the curated base set needs `development.nixLd.enable` |
| `conf.sharing.ssh.enable` | • With `networking.enable`: sshd on port 22, keys only, root login refused, `AllowGroups = [ "ssh-users" ]`<br>• Account groups: `ssh-users` for privileged accounts; others opt in through `groups` |
| `conf.sharing.rdp.enable` | • With `sharing.enable`: GNOME Remote Desktop |
| `conf.users.accounts.<name>.privileged` | • `wheel` (sudo), plus `networkmanager` or `wpa_supplicant`, by backend, `ssh-users`, `docker`, `podman` and `libvirtd` for whichever of those services the host enables. Only `wheel`, `docker`, `podman`, `libvirtd`, `networkmanager` and `wpa_supplicant` are rejected in `groups`; `ssh-users` is not, so others opt in through `groups` |
| `conf.users.accounts.<name>.sudoSshKeys` | • `/etc/ssh/sudo_keys.d/<name>` (root's, mode 0444) with those keys<br>• `security.pam.sshAgentAuth` reading only those files, for sudo<br>• `Defaults noninteractive_auth`, so `sudo -n` can use the key<br>• Listing keys on an unprivileged account is a build error |
| `conf.users.accounts.<name>.secrets.authorizedKeys` | • `users/<name>/authorized_keys` as a secret (the account's, mode 0400) and `/run/secrets/users/%u/authorized_keys` in sshd's `AuthorizedKeysFile`, read at each login<br>• An administrator with these keys only does not trip NixOS's check that someone can log in (`users.allowNoPasswordLogin`, as a default) |
| `conf.users.accounts.<name>.secrets.sudoKeys` | • `users/<name>/sudo_keys` as a secret (root's, mode 0400), `/run/secrets/users/%u/sudo_keys` among the files `security.pam.sshAgentAuth` reads, and the same sudo by key as `sudoSshKeys` gives<br>• On an unprivileged account it is a build error |
| `conf.users.accounts.<name>.secrets.own` | • `users/<name>/all` as a secret: the whole file, the account's, mode 0400, under `/run/secrets`<br>• `fleet-user-secret` on the host, which prints `secrets.<name>` from the file of the user who runs it |
| `conf.fleet.localPath`<br>*(every NixOS host)* | • Trusted in `/etc/gitconfig` (`safe.directory`), so every account can use the root-owned checkout |
| `conf.fleet.repo.url` | • `fleet.autoUpdate.enable` on, `users.mutableUsers = false`<br>• On NixOS: requires `fleet.signing.allowedSignersFile`; plain `http://` is rejected<br>• On NixOS: `networking.firewall.enable = false` is rejected; sshd password authentication gives a warning<br>• An SSH address requires `secrets.repo.url`, whose identity holds the key that fetches it |
| `conf.secrets.repo.url` | • `fleet.support.enable` on, which requires `fleet.support.keysFromSecrets` (and no `fleet.support.sshKeys`) and sets `sharing.ssh.enable = true` (sshd still needs `networking.enable`)<br>• sops-nix reads `<statePath>/secrets`; every account with `secrets.password` (the default) takes its hash from the secrets repository, so `initialHashedPassword` is an error<br>• Requires `fleet.repo.url` (`fleet-bootstrap` and `config-auto-update` refresh the secrets) and `fleet.signing.allowedSignersFile` (every fetched commit is verified against it); an HTTP(S) URL is rejected<br>• Accounts with `secrets.sshKey`: Home Manager points SSH and git signing at `/run/secrets/users/<name>/ssh_key` |
| `conf.fleet.support.keysFromSecrets` | • The `support` account gets `secrets.authorizedKeys`, so its keys are read from its file on the host's branch as well<br>• Required on a host with secrets, where `fleet.support.sshKeys` must be empty; requires `conf.secrets.repo.url` |
| `conf.fleet.support.enable` | • A privileged `support` account with `fleet.support.sshKeys` (or `keysFromSecrets`), a locked password and passwordless sudo<br>• `sharing.ssh.enable = true` (a plain value, so it beats role defaults) |
| `conf.hardware.boot.mode = "bios"` | • `hardware.boot.loader` defaults to `"grub"`; GRUB without `grubPasswordHash` gives a warning |
| `conf.hardware.boot.loader = "uboot"` | • `boot.loader.generic-extlinux-compatible`, with GRUB off and at most 10 generations in the menu<br>• With `hardware.disk.enable`: `/boot` is marked legacy BIOS bootable, where U-Boot looks<br>• `mode = "bios"` or `efiVariables` is a build error<br>• With the splash on there is no menu; the newest generation boots |
| `conf.hardware.boot.splash.enable` | • Plymouth, quiet kernel and systemd output, hidden generation menu<br>• With `hardware.graphics.enable`: the Intel or AMD driver loads in the initrd (early KMS) |
| `conf.hardware.disk.enable` | • Default `fleet`, `swap` and `root` volumes; `fleet` must stay enabled at `/var/lib/fleet`<br>• On a laptop: a warning when `encrypt = false`<br>• Build errors unless `hardware.disk.device` is set, at most one volume is `100%FREE`, and every filesystem volume has a mountpoint |



## An example host

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

## Overriding values

| You want | Use |
|---|---|
| Override a value a module set with `lib.mkDefault`, including every role default | a plain assignment |
| Override a value a module set with a plain assignment | `lib.mkForce`; two plain definitions are a conflict, not a win |
| Beat another module's `lib.mkForce` | `lib.mkOverride 40`; two `mkForce` definitions conflict |
| Append to a list | `extraPackages` is the simplest way where it exists. A plain list assignment adds to other plain definitions, but replaces a list a module set with `lib.mkDefault`; to add to such a list, define yours with `lib.mkDefault` too, so the two merge |

<!-- book:bottom -->
---

[← 3. Building a Raspberry Pi image](raspberry-pi.md) · [Contents](README.md) · [5. Accounts and homes →](accounts.md)
<!-- /book:bottom -->
