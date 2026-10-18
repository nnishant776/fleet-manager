<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet docs

Write the runbook pages, tools/fleet/runbooks, from the flows themselves.

Every command's page says what it is for, lists its flags, and walks through
its steps with the commands they run, all taken from the command's definition.
Run it after changing a flow, and commit the pages with the change; the tests
fail while they are out of date.

```
fleet docs [flags]

fleet docs
fleet docs --out /tmp/runbooks
```

## Flags

| Flag | |
|---|---|
| `--out` | Where the pages go; tools/fleet/runbooks in the checkout this is run in by default |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Write the pages.** One page per command and an index, README.md. Pages that would not change are left alone, and pages of commands that no longer exist are removed.

