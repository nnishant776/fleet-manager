<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet inventory publish

Cut each host's own files from main into its branch, host/\<name>, signed, and push them.

For an inventory whose lib.mkFleet sets hostBranches = true. Each host fetches only
its branch, host/\<name>, which holds flake.nix, flake.lock, fleet/, the host's
folder, its users' folders and the paths listed as shared, and nothing of the
other hosts. fleet checks that main's newest commit is signed by a key you trust
and pushed, cuts each branch from it without touching the checkout, evaluates it,
and adds a signed commit to every branch whose files changed. Then it pushes them,
once you approve. A branch whose newest commit no key you trust signed is not
published on.

```
fleet inventory publish [HOST] [flags]

fleet inventory publish --flake ~/Projects/inventory
fleet inventory publish nas1 --flake ~/Projects/inventory
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host to publish; give --host more than once for several; all of them by default; may be given more than once |
| `--no-check` | Do not evaluate each branch before publishing it; for tests without Nix |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Check main's newest commit.** Every branch is cut from it, so it must be signed by a key you trust and pushed, and the checkout clean.

4. **Cut each host's branch.** With the host's own files only. A branch whose files did not change gets no commit.

5. **Push the branches.** Hosts fetch their branch on their next sync. A push that is not a fast-forward is refused: then someone else changed the branch.

It needs `git`, `nix`, `tar` on the PATH; `fleet doctor` says where to get them.
