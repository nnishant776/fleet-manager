<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet trust signers

Keep your own copy of a checkout's fleet/allowed_signers, which its commits are checked against.

fleet checks commits against your own copy of each repository's signers, kept in
$XDG_STATE_HOME/fleet (~/.local/state/fleet), not against the checkout's file: a
pushed commit could add the key that signs it. The first time, fleet shows the
checkout's keys and keeps them once you approve. After that it takes a change only
when the commit that made it is signed by a key you already trust, and shows the
keys added and removed first.

```
fleet trust signers [flags]

fleet trust signers --flake ~/Projects/inventory
fleet trust signers --flake /etc/nixos
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Read the keys the checkout lists.** Its fleet/allowed_signers, and which repository it is: the copy is kept per repository, named after its address.

2. **Keep your copy of them.** The first copy is the one you approve now. A later change is taken only through a commit signed by a key already in your copy.

It needs `git` on the PATH; `fleet doctor` says where to get them.
