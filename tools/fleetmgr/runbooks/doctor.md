<!-- Written by `fleetmgr docs` from tools/fleetmgr/flows; edit the flows, not this. -->

# fleetmgr doctor

Check this machine has the tools fleetmgr uses.

Lists every program fleetmgr runs and whether it is on PATH, with the nixpkgs
package that provides each missing one. Installed through Nix, fleetmgr brings
all of them but nix itself; the bare static binary relies on the machine's own.

```
fleetmgr doctor [flags]
```

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.

## Steps

1. **Tools on PATH.** fleetmgr shows and runs the same commands you could type, so it needs these programs.

