<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet support-key rotate

Replace the key that opens a host's support account, testing each step.

support has sudo without a password, so its key is root on every host that
lists it. This makes a new key, ideally on a hardware security key (a touch,
and the PIN, on every login), shows the line to add to the host's
configuration next to the old key, syncs the host with the old key, tests the
new one, then has you remove the old line, syncs with the new key, and checks
the old key is refused. Optionally the new line accepts logins only from given
addresses (from=). On a host whose configuration sets
conf.fleet.support.keysFromSecrets, the keys are changed in the host's secrets
(fleet secrets user-keys, pushed) instead of its configuration, and the syncs
apply them without a rebuild.

```
fleet support-key rotate [HOST] [flags]

fleet support-key rotate pi1 --old-key ~/.ssh/admin_key --new-key ~/.ssh/fleet_support --kind hardware
fleet support-key rotate pi1 --old-key ~/.ssh/admin_key --new-key ~/.ssh/fleet_support --from 192.168.0.0/24
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of the configuration (hosts/\<name>) |
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--address` | A name or IP address for SSH; defaults to the host's name |
| `--old-key` | Its private key file |
| `--new-key` | A private key file; made unless it exists |
| `--kind` | hardware (a FIDO2 security key: touch and PIN) or software (a passphrase); hardware by default |
| `--from` | Addresses or networks for sshd's from=, such as 192.168.0.0/24; empty for anywhere |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Make the new key.** A hardware key keeps the private half on the device: every login needs a touch, and its PIN. A software key gets a passphrase.

4. **Add the new key next to the old one.** Both are accepted for now, so the host can be reached whichever way the next steps go.

5. **Sync HOST with the old key.** It picks up the configuration that accepts both keys.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i OLD-KEY support@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

6. **Test the new key.** With the new key only and nothing else offered: it must log in, and sudo must work.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i NEW-KEY support@ADDRESS 'sudo -n id -u'
   ```

7. **Remove the old key.** Until it is gone, whoever holds it is still root on HOST.

8. **Sync HOST with the new key.** It picks up the configuration without the old key.

   ```
   $ ssh -o ForwardAgent=no -o BatchMode=yes -o ConnectTimeout=10 -o IdentitiesOnly=yes -o IdentityAgent=none -i NEW-KEY support@ADDRESS 'sudo -n systemctl start config-auto-update'
   ```

9. **Check the old key is refused.** The rotation is done only when the old key no longer opens support.

It needs `ssh`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
