<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet doctor

Check this machine has the tools fleet uses.

Lists every program fleet runs and whether it is on PATH, with the nixpkgs
package that provides each missing one. Installed through Nix, fleet brings
all of them but nix itself; the bare static binary relies on the machine's own.

```
fleet doctor [flags]
```

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.

## Steps

1. **Tools on PATH.** fleet shows and runs the same commands you could type, so it needs these programs.

