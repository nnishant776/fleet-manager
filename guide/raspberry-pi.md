# Building a Raspberry Pi image

One generic SD image for every Raspberry Pi 5, given each card's own settings on its FAT partition.

A utility, run by hand when needed; nothing in a rebuild or the sync uses it.

```bash
nix run .#rpi-image                     # writes ./nixos-rpi5.img.zst
nix run .#rpi-image -- -o pi.img.zst
nix run .#rpi-image -- --flake <reference>   # build from another flake
```

It builds one generic Raspberry Pi 5 SD image from the installer in
`nixos-raspberrypi`, plus a first-boot step. Build it once, flash it to as many
cards as needed, then give each card its own settings by creating files in
`firstboot/` on its FAT partition, which any computer can write:

| File | Effect on first boot |
|---|---|
| `hostname` | the Pi's hostname (default: `rpi-` and the last four characters of the board serial) |
| `authorized_keys` | SSH keys for `root` and the `nixos` user |
| `wifi` | `SSID=` and `PSK=` lines (password 8 to 63 characters; no `PSK=` for an open network), and a `COUNTRY=` line with your two-letter country code, such as `IN`. Without a country, much of 5 GHz is unusable and the Pi may stay on 2.4 GHz. Added to the Wi-Fi networks in `/etc/wpa_supplicant/imperative.conf`, then deleted from the card |

A `README.txt` on the card repeats this. After the first boot, log in over SSH
and switch the Pi to its host configuration; from then on it updates from this
flake like any other host and the image is not needed again.

The card itself uses systemd-networkd and wpa_supplicant. To keep the same
Wi-Fi after the switch, give the host `conf.networking.backend = "networkd"`
with Wi-Fi on, since it reads the same network file. If the first boot reports
a problem with the wifi file, `journalctl -u rpi-firstboot` says what.

Building needs `aarch64-linux`: natively, through emulation
(`boot.binfmt.emulatedSystems = [ "aarch64-linux" ];` on a NixOS host), or with
a remote builder. The vendor kernel comes prebuilt from the nixos-raspberrypi
binary cache, which Nix only uses for trusted users, so run it with `sudo`
unless your user is in `nix.settings.trusted-users`; otherwise the kernel is
compiled, which under emulation takes hours.
