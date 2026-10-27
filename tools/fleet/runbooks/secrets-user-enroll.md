<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet secrets user-enroll

List a user on hosts and move their files to a branch of their own.

Takes the user's enrollment file (version, user, age_recipient, signing_key) and gives the user a branch,
user/\<user>, which holds that file as enroll and, for each host, users/\<user>/\<host>.yaml encrypted to
the user, the host and your recovery recipient. The hosts are every host whose branch holds a file of
the user's, and each --host: a file there is re-encrypted with the user's recipient added and moved
to the user's branch, and a host without one gets a new, empty file. On each host's branch the user is
listed in hosts/\<host>.yaml (users.\<user>, encrypted: their recipient and signing key) and the file is
removed. Commits (user-enroll: \<user>, and user-enroll: \<user> on \<host>) and pushes user/\<user> first,
then each host's branch. The same identity again changes nothing; another identity is refused. Hosts
must run a sync that reads user branches before a user is enrolled, or they lose the user's files.

```
fleet secrets user-enroll [USER] [flags]

fleet secrets user-enroll alice --from alice.enroll
fleet secrets user-enroll alice --from alice.enroll --host laptop1 --host nas
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to FLEET_SECRETS_REPO, then this directory |
| `--recovery` | Your passphrase-encrypted age identity (age -p). Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--user` | The user's name: lowercase letters, digits and '-' |
| `--from` | The user's enrollment file (version, user, age_recipient and signing_key), or - for standard input |
| `--host` | A host to list the user on, whether or not its branch holds a file of theirs; may be given more than once; may be given more than once |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Open the secrets repository.** The change builds on the host's branch and encrypts for the recipients that branch names, so git must check its signatures against keys a pushed commit cannot change. New files are encrypted to the host and to your recovery recipient, which fleet setup caches.

   ```
   $ cd REPO && git rev-parse --git-dir
   $ cd REPO && sh -c 'f="$(git config --get gpg.ssh.allowedSignersFile)" || exit 0
case "$f" in \~/*) f="$HOME/${f#\~/}" ;; esac
case "$f" in /*) ;; *) f="$(git rev-parse --show-toplevel)/$f" ;; esac
if git -C "$(dirname "$f")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "inside $f"; fi'
   $ cd REPO && git config --local --default '' --get core.hooksPath
   ```

3. **Read the enrollment file.** It holds the user's two public keys: the age recipient their files are encrypted to, and the key their commits are signed with.

4. **Look up user/USER.** A user's branch is user/\<name>; a user that has one is enrolled. The same identity again changes nothing there; another identity is re-keyed, not enrolled over.

5. **Unlock your recovery identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d RECOVERY
   ```

6. **Find the hosts and what is left to do on them.** Every host whose branch holds a file of USER's, and each --host. The lists in their files are encrypted, so this needs your recovery identity. A host that lists the user with another identity stops the run.

7. **Write user/USER.** In a temporary worktree: the enrollment file as given, and for each host the user's file, re-encrypted with the user's recipient added when the host's branch had one and new and empty when it had none. Then one commit.

8. **List USER on the hosts and take their files off the hosts' branches.** In a temporary worktree of each host's branch: users.\<user> in hosts/\<host>.yaml, which is encrypted, and the file removed. One commit per host.

9. **Push user/USER.** First, so that the hosts' branches never point at a user branch that is not there.

   ```
   $ cd REPO && git push origin user/USER
   ```

10. **Push the hosts' branches.** A host reads the user from its branch on its next sync, which checks the commit is signed by an allowed key.

It needs `git`, `yq`, `sops`, `age`, `ssh-keygen` on the PATH; `fleet doctor` says where to get them.
