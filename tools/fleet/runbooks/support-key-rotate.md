<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet support-key rotate

Replace the key that opens a host's support account, testing each step.

support has sudo without a password, so its key is root on every host that
lists it. This makes a new key, ideally on a hardware security key (a touch,
and the PIN, on every login), shows the line to add to the host's
configuration next to the old key, syncs the host with the old key, tests the
new one, then has you remove the old line, syncs with the new key, and checks
the old key is refused. Optionally the new line accepts logins only from given
addresses (from=).

```
fleet support-key rotate [HOST] [flags]

fleet support-key rotate neerajpinew --old-key ~/.ssh/home_network_ssh_key --new-key ~/.ssh/fleet_support --kind hardware
fleet support-key rotate neerajpinew --old-key ~/.ssh/home_network_ssh_key --new-key ~/.ssh/fleet_support --from 192.168.0.0/24
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--old-key` | Its private key file |
| `--new-key` | A private key file; made unless it exists |
| `--kind` | hardware (a FIDO2 security key: touch and PIN) or software (a passphrase); hardware by default |
| `--from` | Addresses or networks for sshd's from=, such as 192.168.0.0/24; empty for anywhere |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Make the new key.** A hardware key keeps the private half on the device: every login needs a touch, and its PIN. A software key gets a passphrase.

2. **Add the new key next to the old one.** Both are accepted for now, so the host can be reached whichever way the next steps go.

3. **Sync HOST with the old key.** It picks up the configuration that accepts both keys.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i OLD-KEY support@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

4. **Test the new key.** With the new key only and nothing else offered: it must log in, and sudo must work.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i NEW-KEY support@ADDRESS 'sudo -n id -u'
   ```

5. **Remove the old key.** Until it is gone, whoever holds it is still root on HOST.

6. **Sync HOST with the new key.** It picks up the configuration without the old key.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i NEW-KEY support@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

7. **Check the old key is refused.** The rotation is done only when the old key no longer opens support.

It needs `ssh`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
