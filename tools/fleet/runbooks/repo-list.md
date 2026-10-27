<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet repo list

List the names of the tools' clones, and the commits you trust in each.

Each name that fleet repo add made, with its repository, address and clone, the
commit the clone stands at, and the commit you trust for each branch. Repositories
that have trusted commits or a copy of their keys but no name are listed apart.

```
fleet repo list [flags]

fleet repo list
```

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.

## Steps

1. **List them.** What is kept in ~/.local/state/fleet (names, trusted commits, keys) and ~/.local/share/fleet (the clones).

