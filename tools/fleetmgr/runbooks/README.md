<!-- Written by `fleetmgr docs` from tools/fleetmgr/flows; edit the flows, not this. -->

# fleetmgr runbooks

One page per command: what it is for, its flags, and its steps with the
commands they run. `fleetmgr` shows the same as it goes, and with `--dry-run` it
shows every step and command for a real host and changes nothing. `fleetmgr` with
no arguments offers these as a menu.

| Command | What it does |
|---|---|
| [`fleetmgr host check`](host-check.md) | Check a host: configuration, sync, secrets and health, without changing anything |
| [`fleetmgr host sync`](host-sync.md) | Have a host fetch its configuration and secrets now, rather than at its next timer, and check what it then runs |
| [`fleetmgr host secret`](host-secret.md) | Add or change a host secret: encrypt, commit, push, and show how the configuration uses it |
| [`fleetmgr host enroll`](host-enroll.md) | Enroll a running host: identity, enrollment, deploy key, first sync |
| [`fleetmgr support-key rotate`](support-key-rotate.md) | Replace the key that opens a host's support account, testing each step |
| [`fleetmgr inventory pin`](inventory-pin.md) | Pin fleet-manager's newest trusted commit, or a given one, in the inventory, check every host, commit and push |
| [`fleetmgr inventory import`](inventory-import.md) | Copy a host's definition from fleet-manager into the inventory, adjusted, check it, commit, push and publish its branch |
| [`fleetmgr host adopt`](host-adopt.md) | Store the password and keys of the owner a personal machine made at its first boot, and print the declaration to add |
| [`fleetmgr inventory publish`](inventory-publish.md) | Cut each host's own files from main into its branch, host/\<name>, signed, and push them |
| [`fleetmgr install`](install.md) | Install a host from the configuration onto this machine's disks, from the installer |
| [`fleetmgr install prep`](install-prep.md) | Check the commits a host's install takes, and print the line to run on the installer |
| [`fleetmgr setup`](setup.md) | Set up this machine as the maintainer's: recovery identity, signing key, both checkouts |
| [`fleetmgr trust signers`](trust-signers.md) | Keep your own copy of a checkout's fleet/allowed_signers, which its commits are checked against |
| [`fleetmgr repo add`](repo-add.md) | Clone a repository for the tools to work in, check its keys and main, and give it a name |
| [`fleetmgr repo list`](repo-list.md) | List the names of the tools' clones, and the commits you trust in each |
| [`fleetmgr repo sync`](repo-sync.md) | Fetch the tools' clones, show the new commits and who signed them, and accept them |
| [`fleetmgr doctor`](doctor.md) | Check this machine has the tools fleetmgr uses |
| [`fleetmgr secrets user-password`](secrets-user-password.md) | Set the user's password on that host |
| [`fleetmgr secrets user-key`](secrets-user-key.md) | Generate the user's SSH key on that host |
| [`fleetmgr secrets user-keys`](secrets-user-keys.md) | Set the SSH keys that may log in as the user, or unlock their sudo, on that host |
| [`fleetmgr secrets user-enroll`](secrets-user-enroll.md) | List a user on hosts and move their files to a branch of their own |
| [`fleetmgr secrets user-retire`](secrets-user-retire.md) | Take a user off the hosts that list them and delete their branch |
| [`fleetmgr secrets host-enroll`](secrets-host-enroll.md) | Admit a new host from its enrollment file |
| [`fleetmgr secrets host-rekey`](secrets-host-rekey.md) | Point a known host at a new identity |
| [`fleetmgr secrets host-retire`](secrets-host-retire.md) | Remove a host and its files |
| [`fleetmgr user init`](user-init.md) | Make your age identity, your signing key and the enrollment file to give the maintainer |
| [`fleetmgr user password`](user-password.md) | Set your password on hosts, on your own branch |
| [`fleetmgr user keys`](user-keys.md) | Set the SSH keys that may log in as you, or unlock your sudo, on hosts, on your own branch |
| [`fleetmgr user secret set`](user-secret-set.md) | Store a secret of your own, which your hosts hand only to you, on your own branch |
| [`fleetmgr user secret unset`](user-secret-unset.md) | Remove a secret of your own from your files, on your own branch |
| [`fleetmgr docs`](docs.md) | Write the runbook pages, tools/fleetmgr/runbooks, from the flows themselves, and the parts of the guide that follow from its order and headings |
