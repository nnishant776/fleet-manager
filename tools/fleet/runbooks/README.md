<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet runbooks

One page per command: what it is for, its flags, and its steps with the
commands they run. `fleet` shows the same as it goes, and with `--dry-run` it
shows every step and command for a real host and changes nothing. `fleet` with
no arguments offers these as a menu.

| Command | What it does |
|---|---|
| [`fleet host check`](host-check.md) | Check a host: configuration, sync, secrets and health, without changing anything |
| [`fleet host secret`](host-secret.md) | Add or change a host secret: encrypt, commit, push, and show how the configuration uses it |
| [`fleet host enroll`](host-enroll.md) | Enroll a running host: identity, enrollment, deploy key, first sync |
| [`fleet support-key rotate`](support-key-rotate.md) | Replace the key that opens a host's support account, testing each step |
| [`fleet install`](install.md) | Install a host from the configuration onto this machine's disks, from the installer |
| [`fleet install prep`](install-prep.md) | Check the commits a host's install takes, and print the line to run on the installer |
| [`fleet setup`](setup.md) | Set up this machine as the maintainer's: recovery identity, signing key, both checkouts |
| [`fleet doctor`](doctor.md) | Check this machine has the tools fleet uses |
| [`fleet secrets user-password`](secrets-user-password.md) | Set the user's password on that host |
| [`fleet secrets user-key`](secrets-user-key.md) | Generate the user's SSH key on that host |
| [`fleet secrets host-enroll`](secrets-host-enroll.md) | Admit a new host from its enrollment file |
| [`fleet secrets host-rekey`](secrets-host-rekey.md) | Point a known host at a new identity |
| [`fleet secrets host-retire`](secrets-host-retire.md) | Remove a host and its files |
| [`fleet docs`](docs.md) | Write the runbook pages, tools/fleet/runbooks, from the flows themselves |
