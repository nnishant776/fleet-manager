<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet host check

Check a host: configuration, sync, secrets and health, without changing anything.

Logs in to the host as support, with nothing forwarded, and reports what it
runs, whether its last sync succeeded, whether a problem is recorded for
everyone to see, what its secrets snapshot holds, and whether any unit failed.
It changes nothing, so it never asks.

```
fleet host check [HOST] [flags]

fleet host check neerajpinew
fleet host check pi-gw --address 192.168.0.9
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos, then github:nnishant776/fleet-manager |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--user` | The account to log in as; support by default, which has sudo without a password, so nothing needs forwarding |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

Every step runs, whatever an earlier one found; the command fails at the end
if any found a problem.

1. **Reach HOST.** As USER, with no agent forwarded, and only to a host whose key this machine already knows.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'id -un; hostname'
   ```

2. **Configuration and sync.** The running system should be the revision its checkout holds, and the last sync should have succeeded.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'r=$(cat /etc/org/revision); h=$(git -C /etc/nixos rev-parse HEAD 2>/dev/null || echo none); echo "$r $h"'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'test -d /etc/nixos/.git || { echo none; exit 0; }; git -C /etc/nixos status --porcelain | wc -l'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'systemctl show -p LoadState,Result,ExecMainExitTimestamp --value config-auto-update.service'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'cat /var/lib/fleet-sync/status 2>/dev/null || true'
   ```

3. **Secrets.** The snapshot names the secrets commit the host last accepted; /run/secrets holds what activation decrypted.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n test -d /var/lib/fleet || { echo none; exit 0; }; sudo -n readlink /var/lib/fleet/secrets || echo missing'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n find -H /run/secrets /run/secrets-for-users -mindepth 1 ! -type d -printf '\''%P\n'\'' 2>/dev/null | sort -u'
   ```

4. **Health.** A failed unit is worth knowing about before changing anything else.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'systemctl is-system-running || true; systemctl --failed --no-legend --plain | cut -d'\'' '\'' -f1'
   ```

It needs `ssh`, `nix` on the PATH; `fleet doctor` says where to get them.
