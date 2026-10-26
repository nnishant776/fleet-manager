<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet user init

Make your age identity, your signing key and the enrollment file to give the maintainer.

Once per user: a passphrase-encrypted age identity, ~/.fleet/user.age, which decrypts your files in the
secrets repository; an SSH key, ~/.ssh/fleet_user_signing_key, that signs your commits to your branch,
user/\<user>; and ~/.fleet/\<user>.enroll, which holds the public halves of both (the age recipient and
the signing key) and nothing secret. Give the enrollment file to the maintainer, who runs fleet secrets
user-enroll. The identity is written straight into a passphrase-encrypted file and never touches the disk in
clear; keep a copy offline. Each step that is done already is skipped, so it is safe to run again.

```
fleet user init [flags]

fleet user init
fleet user init --user alice
```

## Flags

| Flag | |
|---|---|
| `--user` | The user's name: lowercase letters, digits and '-'; your login name by default |
| `--identity` | Where your passphrase-encrypted age identity (age -p) is, or goes; ~/.fleet/user.age by default |
| `--signing-key` | The SSH private key that signs your commits to your branch; ~/.ssh/fleet_user_signing_key by default |
| `--enroll` | Where your enrollment file is, or goes; ~/.fleet/\<user>.enroll by default |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Create your identity.** It decrypts your files, so it is written straight into a passphrase-encrypted file and never touches the disk in clear. age asks for the passphrase twice. Keep a copy offline.

   ```
   $ sh -c 'umask 077 && mkdir -p "$(dirname "$1")" && age-keygen | age -p -o "$1"' sh IDENTITY
   ```

2. **Create your signing key.** An SSH key that signs every commit to your branch; hosts accept your commits only if it signed them. Give it a passphrase, and load it into your agent with ssh-add when you work.

   ```
   $ ssh-keygen -t ed25519 -a 100 -C 'USER fleet signing' -f SIGNING-KEY
   ```

3. **Write your enrollment file.** The maintainer needs the public halves: your age recipient, derived from the identity (age asks for the passphrase once; nothing is written in clear), and your signing key's type and material.

4. **Give the enrollment file to the maintainer.** It holds public keys only, so any channel will do. The maintainer lists you on your hosts and moves your files to your branch.

   > Give the maintainer ENROLL. They run:
   > fleet secrets user-enroll USER --from USER.enroll
   > 
   > Then your own commands work on your branch: fleet user password, fleet user keys, fleet user secret set.

It needs `age`, `age-keygen`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
