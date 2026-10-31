<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet host sync

Have a host fetch its configuration and secrets now, rather than at its next timer, and check what it then runs.

Logs in to the host as support, with nothing forwarded, and starts its sync
at once, instead of waiting for its timer: the host fetches its branch and its
secrets, checks that they are signed by a key it trusts, and switches to what
it gets. fleet waits for the sync to end, shows the host's log if it failed,
and checks that the host now runs the newest commit of its branch. Then it
reports the same as fleet host check: configuration, secrets and health.
It stops at the first step that fails.

```
fleet host sync [HOST] [flags]

fleet host sync pi1
fleet host sync pi-gw --address 192.168.0.9
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--user` | The account to log in as; support by default, which has sudo without a password, so nothing needs forwarding |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Reach HOST.** As USER, with no agent forwarded, and only to a host whose key this machine already knows.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'id -un; hostname'
   ```

4. **See what it runs and what is published.** The sync should take the host to the newest commit of the branch it tracks; the host's revision now, and that commit, are what the check afterwards compares.

5. **Sync it.** Starts the host's sync and waits until it ends. It fetches the configuration and the secrets, and activation applies them; a commit no key it trusts signed is refused.

6. **Check what it runs now.** After the sync the host should run the commit the branch was at.

7. **Configuration and sync.** The running system should be the revision its checkout holds, and the last sync should have succeeded.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'r=$(cat /etc/org/revision); h=$(git -C /etc/nixos rev-parse HEAD 2>/dev/null || echo none); echo "$r $h"'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'test -d /etc/nixos/.git || { echo none; exit 0; }; git -C /etc/nixos status --porcelain | wc -l'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'systemctl show -p LoadState,Result,ExecMainExitTimestamp --value config-auto-update.service; m=$(systemctl show -p ExecMainExitTimestampMonotonic --value config-auto-update.service); echo "age $m $(cut -d'\'' '\'' -f1 /proc/uptime)"'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'systemctl cat config-auto-update.timer >/dev/null 2>&1 || { echo none; exit 0; }; systemctl is-active config-auto-update.timer || true; systemctl is-enabled config-auto-update.timer 2>/dev/null || true'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'cat /var/lib/fleet-sync/status 2>/dev/null || true'
   ```

8. **Secrets.** The snapshot names the secrets commit the host last accepted; /run/secrets holds what activation decrypted.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n test -d /var/lib/fleet || { echo none; exit 0; }; if sudo -n readlink /var/lib/fleet/secrets; then sudo -n cat /var/lib/fleet/secrets/manifest 2>/dev/null || true; else echo missing; fi'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'cat /var/lib/fleet-sync/user-problems 2>/dev/null || true'
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'sudo -n find -H /run/secrets /run/secrets-for-users -mindepth 1 ! -type d -printf '\''%P\n'\'' 2>/dev/null | sort -u'
   ```

9. **Health.** A failed unit is worth knowing about before changing anything else.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 USER@ADDRESS 'systemctl is-system-running || true; systemctl --failed --no-legend --plain | cut -d'\'' '\'' -f1'
   ```

It needs `ssh`, `git`, `nix` on the PATH; `fleet doctor` says where to get them.
