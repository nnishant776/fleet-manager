<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet install prep

Check the commits a host's install takes, and print the line to run on the installer.

Run on your machine, in the configuration checkout, before installing a host.
fleet checks that the checkout's newest commit is signed by a key in its
fleet/allowed_signers and pushed, and so is the fleet-manager commit its
flake.lock pins, which the installer comes from. It then prints one line to run
on the machine booted from the NixOS installer: it fetches bootstrap by that
commit, checks it against a checksum worked out here, and installs exactly
those commits, so that a push to main since cannot change what gets installed.

```
fleet install prep [HOST] [flags]

fleet install prep nas1 --flake ~/Projects/inventory
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

2. **Check the configuration commit.** It is what gets installed: the checkout's newest commit, which must be signed by a key in its fleet/allowed_signers, and pushed, since the installer fetches it.

3. **Check the fleet-manager commit the installer comes from.** bootstrap, fleet and os-install come from fleet-manager. It is the commit the configuration's flake.lock pins, fetched with git and checked against the same signers file.

4. **Run this on the installer.** On the machine booted from the NixOS installer. Typing it is error-prone: set a password there with passwd, then ssh nixos@\<its address> from here and paste it. Before you answer ssh's question about the host key, compare the fingerprint it shows with the one the installer's own screen shows (ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub there): something else on the network could pose as it, and would get the disk passphrase you type.

It needs `git`, `nix` on the PATH; `fleet doctor` says where to get them.
