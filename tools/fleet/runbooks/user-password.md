<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet user password

Set your password on hosts, on your own branch.

Hashes the password (yescrypt) and stores the hash in your file for each host, users/\<user>/\<host>.yaml,
on your branch, user/\<user>, of your clone of the secrets repository; commits, signed with your own signing key,
and pushes the branch. Hosts apply the password at their next sync. The branch's newest commit must be signed
by your key or by a key in your copy of the configuration repository's signers before the change builds on it.

```
fleet user password [HOST]... [flags]

fleet user password laptop1 nas
fleet user password --all-hosts
printf %s "$pw" | fleet user password laptop1 --password-stdin --yes
```

## Flags

| Flag | |
|---|---|
| `--repo` | Your clone of the secrets repository, left on main; defaults to FLEET_SECRETS_REPO, then this directory |
| `--user` | The user's name: lowercase letters, digits and '-'; your login name by default |
| `--password-stdin` | read the password from standard input, instead of being asked. Hidden as you type |
| `--host` | A host whose file of yours is changed; may be given more than once. Or use --all-hosts; may be given more than once |
| `--all-hosts` | Change your file on every host that has one on your branch, instead of the hosts named |
| `--identity` | Your passphrase-encrypted age identity (age -p); ~/.fleet/user.age by default. Empty: sops uses SOPS_AGE_KEY_FILE or SOPS_AGE_KEY |
| `--signing-key` | The SSH private key that signs your commits to your branch; ~/.ssh/fleet_user_signing_key by default |
| `--enroll` | Where your enrollment file is, or goes; ~/.fleet/\<user>.enroll by default |
| `--flake` | A checkout of the configuration repository, whose origin names your copy of its signers (fleet trust signers); defaults to this directory's flake, then /etc/nixos. Not needed with --signers |
| `--signers` | An allowed_signers file to check your branch's newest commit against, besides your own key; by default your copy of the configuration repository's |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

2. **Hash the password.** Only the yescrypt hash is stored; the password itself goes nowhere.

   ```
   $ mkpasswd -m yescrypt --stdin   # the password on stdin
   ```

3. **Open the secrets repository.** Your clone of it; your branch, user/USER, is changed in a temporary worktree of it, so that your checkout stays as it is.

   ```
   $ cd REPO && git rev-parse --git-dir
   ```

4. **Fetch user/USER from origin.** To build on the newest commit of your branch, which the maintainer or you may have added to since.

   ```
   $ cd REPO && git fetch -q origin +refs/heads/user/USER:refs/remotes/origin/user/USER
   ```

5. **Find your branch.** Your clone's user/USER, or else origin's. A local branch that origin's has moved on from is brought up to it; one with commits origin lacks is used as it is.

6. **Check who signed its newest commit.** You build on it, so it must be signed by your own key or by a key in your copy of the configuration repository's signers (the maintainers'): a pushed commit by anyone else is not to be built on.

7. **Choose the hosts.** The hosts named, each of which must have a file of yours on your branch (the maintainer's fleet secrets user-enroll makes them), or all of them with --all-hosts.

8. **Unlock your identity.** Decrypted into this run's memory only, for the sops commands below. age asks for its passphrase.

   ```
   $ age -d IDENTITY
   ```

9. **Open a worktree of BRANCH.** The change is made in a temporary worktree of the branch, so that your checkout stays on main and the commit holds this change and nothing else. It goes away at the end, or when the run stops.

10. **Store the hash in your files.** As the key password, which hosts read when accounts.\<user>.secrets.password is on.

11. **Commit, signed with your key.** Signed with your own signing key, which must be in your agent, or ssh-keygen asks for its passphrase; hosts accept your commit only if it is signed by it. Your git configuration is not touched.

12. **Remove the worktree.** It was only for this change; the commit is on BRANCH in your clone.

   ```
   $ cd REPO && git worktree remove --force WORK
   ```

13. **Push BRANCH.** Your hosts fetch your branch on their next sync, which checks the commit is signed by you, and that it is encrypted to the right recipients.

   ```
   $ cd REPO && git push origin BRANCH
   ```

It needs `git`, `sops`, `age`, `ssh-keygen`, `mkpasswd` on the PATH; `fleet doctor` says where to get them.
