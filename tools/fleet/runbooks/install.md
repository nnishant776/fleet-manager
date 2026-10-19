<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet install

Install a host from the configuration onto this machine's disks, from the installer.

Run on a machine booted from the NixOS installer. fleet checks the network,
helps pick the host and the disks to erase, says what os-install will ask for,
then runs os-install, which erases the disks, installs, and creates the host's
identity when it fetches secrets. Afterwards it checks the new disk and lists
what follows the first boot. The flags are os-install's.

```
fleet install [HOST] [flags]

fleet install
fleet install -H laptop1 -d /dev/nvme0n1
fleet install -H nas1 -d main=/dev/nvme0n1 -d data=/dev/sda --enroll-out /media/usb/nas1.enroll
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos, then github:nnishant776/fleet-manager |
| `-H`, `--host` | The host to install (hosts/\<name>); chosen from a list when not given |
| `-d`, `--disk` | [NAME=]DEVICE for a disk of the host's layout, as os-install takes it; chosen from a list when not given; may be given more than once |
| `--identity` | An identity document from 'fleet-identity --export' on the old install, instead of a new identity |
| `--no-enroll` | Do not wait for the host to be enrolled; it retries after boot |
| `--enroll-out` | A path, for example on a mounted USB stick |
| `--private` | The flake is a private repository, git+ssh://…, that the host reads with its own key (conf.fleet.repo.hostKey); os-install, from fleet-manager, makes its identity first and waits for its keys to be registered. Set for a git+ssh:// flake |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Check the network.** Everything comes over the network: the configuration and fleet-manager, and the packages from the binary cache. A cable usually works by itself; Wi-Fi needs joining.

2. **Choose the host.** Each directory under hosts/ is one machine's configuration, and the name it will have.

3. **Look at HOST.** What os-install will do: the disks the layout names, whether they are encrypted, and whether the host needs enrolling. os-install reads the same again.

4. **Choose the disks.** Each disk the layout names needs a device on this machine. Everything on those devices is erased. The disk the installer runs from is never offered.

5. **Install HOST.** os-install does the rest, and asks for what it needs: the host name typed out before it erases anything, and the disk passphrase twice when the disks are encrypted. It partitions, formats and mounts the disks at /mnt, installs onto them, and creates the host's identity when it fetches secrets.

6. **Check the new disk.** What is on it before the reboot: the installed system and, for a host that fetches secrets, its identity.

7. **Reboot.** The machine is ready to start from its own disk.

It needs `nix` on the PATH; `fleet doctor` says where to get them.
