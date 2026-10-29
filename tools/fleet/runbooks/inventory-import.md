<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet inventory import

Copy a host's definition from fleet-manager into the inventory, adjusted, check it, commit, push and publish its branch.

A host that fleet-manager defines can move to a private inventory. This copies what
the host is made of: its folder in hosts/, and the folders in users/ of the accounts it
declares. It reads them from a commit of fleet-manager, never from a working tree: the
tool's own clone of it by default (brought up to date, which only takes commits signed
by a key you trust), or the checkout you give with --from, whose newest commit must be
signed by a key you trust.

The copy is adjusted in three ways and no other. The path to fleet/allowed_signers gets
one level deeper, since the inventory keeps hosts one folder lower. The address the host
fetches its configuration from, fleet-manager's, becomes the inventory's: --address, else
the one the inventory's other hosts use, else the inventory's origin. And the host must not
set conf.fleet.repo.ref, which the inventory sets to the host's own branch. A file that
reads something outside the host's folder stops the move, naming it, since the inventory
would not have it.

fleet shows each change, then checks the host in the inventory: it fetches the inventory's
address, has its branch, its signers file is the inventory's, and every host still
evaluates. If any of that fails, the copy is taken out again. Then it commits, signed,
pushes to main once you approve, and publishes the host's branch. The machine itself
still runs from fleet-manager: the last step lists the commands that finish the move.

```
fleet inventory import [HOST] [flags]

fleet inventory import nas1 --flake inventory
fleet inventory import nas1 --from ~/Projects/fleet-manager --flake inventory --address git@example.com:org/inventory.git
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--host` | A host of fleet-manager (hosts/\<name>) |
| `--from` | A fleet-manager checkout, or a repository name; the tools' own clone of fleet-manager by default |
| `--address` | The address hosts fetch the inventory from; by default the one the inventory's other hosts use |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Check the inventory.** The host is committed on top of the inventory's main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. Its layout says where hosts and users go, and the host must not be in it yet.

4. **Read the host from fleet-manager.** The host's files come from a commit of fleet-manager that a key you trust signed, not from a working tree. The accounts the host declares are read from that commit too, and the folders of those in users/ come along.

5. **See what the host reads outside its folder.** The inventory has only what is copied. A file that reads something outside the host's folder, other than the signers file and the users that come along, would not find it there, so the move stops and names it.

6. **Copy it, adjusted.** Writes the host's folder and the users' folders into the inventory, with the signers path, the address and nothing else changed. Each change is shown first. If the run stops before the commit, what was written is taken out again.

7. **Check it in the inventory.** The host is evaluated there, as the hosts will see it: the address it fetches, its branch, the signers file it uses, and that every host still evaluates. A failure takes the copy out again.

8. **Commit it.** One commit that adds the host's folders. The repository's own git settings sign it.

9. **Push it.** Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first. What you push is the commit you trust on main from then on.

10. **Publish its branch.** The host fetches its own branch, host/HOST, not main. Only that branch is cut, checked to build from its own files alone, signed and pushed.

11. **Next.** The inventory has the host now, but the machine still runs from fleet-manager. These finish the move.

It needs `git`, `nix`, `tar` on the PATH; `fleet doctor` says where to get them.
