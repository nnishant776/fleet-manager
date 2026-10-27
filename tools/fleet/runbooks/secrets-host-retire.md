<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets host-retire

Remove a host and its files.

Deletes the host's branch, host/\<host>, in your clone after showing what it held, and lists what
is still to do: deleting it on the forge, and the host's deploy key. Makes no commit.

```
fleet secrets host-retire [HOST] [flags]

fleet secrets host-retire laptop1
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--host` | The host's name: lowercase letters, digits and '-' |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

2. **Look up host/HOST.** A host's branch is host/\<name>; a host that has none is not enrolled.

3. **Show what host/HOST holds.** The files that go with the branch.

   ```
   $ cd REPO && git ls-tree -r --name-only REF
   ```

4. **Delete the local branch host/HOST.** With it go the host's files and its users' files on it.

   ```
   $ cd REPO && git branch -D -q host/HOST
   ```

5. **Forget the commit you trusted on host/HOST.** The branch is going away, so the record of its last trusted commit (a file in your fleet state folder) goes too; if the host is enrolled again, its branch is trusted afresh.

6. **Still to do.** The branch is gone from your clone only; the host's access ends when its deploy key goes.

   > HOST is retired. Still to do:
   > 1. Delete the branch on the forge, which takes it away from the host:
   > git push origin --delete host/HOST
   > 2. Delete the host's deploy key on the forge; that takes effect at once.
   > 3. Rotate what the host could decrypt, which is only its own files:
   > its users' passwords and SSH keys on HOST (revoke their public keys
   > on the forge now) and whatever was in hosts/HOST.yaml.

It needs `git` on the PATH; `fleet doctor` says where to get them.
