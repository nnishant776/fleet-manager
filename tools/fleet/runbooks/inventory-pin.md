<!-- Written by `fleet docs` from tools/fleet/flows; edit the flows, not this. -->

# fleet inventory pin

Pin fleet-manager's newest trusted commit, or a given one, in the inventory, check every host, commit and push.

An inventory builds its hosts from fleet-manager, at the commit its flake.lock pins.
This moves that pin. fleet brings its clone of fleet-manager up to date, which only
takes a commit signed by a key you trust, and pins main's newest commit, or the one
you give with --to. It shows the commits the pin moves over, and asks before it pins
an older one. Then it locks the new commit and evaluates every host, so that a host
the new fleet-manager breaks is found now, not on the host. If all of them evaluate,
it commits flake.lock, signed, and pushes it once you approve. The hosts take the new
fleet-manager when you publish the inventory after that.

```
fleet inventory pin [flags]

fleet inventory pin --flake fleet-inventory
fleet inventory pin --to 3f2a9c1 --flake fleet-inventory
```

## Flags

| Flag | |
|---|---|
| `--flake` | A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; defaults to this directory's flake, then /etc/nixos |
| `--to` | A fleet-manager commit or branch to pin; its newest main by default |

Every command also takes `--dry-run` (show every step and command; look, but
change nothing), `-y`/`--yes` (run changes without asking) and `--verbose`.
A value that is neither given nor has a default is asked for on a terminal.

## Steps

1. **Bring the tool's clone up to date.** You named a repository, so the command works in the tool's own clone of it. It is fetched, and main's newest commit is taken only if it is signed by a key you trust and follows the commit you trusted last; the clone then stands there.

2. **Compare the checkout's keys with the ones you trust.** Commits are checked against your own copy of each repository's keys, not against the checkout's fleet/allowed_signers. A difference means the keys changed since you trusted them: expected if you changed them, worth a look if not.

3. **Check the inventory.** The pin is committed on top of its main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. The pin it holds now is read from flake.lock.

4. **Bring fleet-manager's clone up to date.** The new pin is taken from the tool's own clone of fleet-manager, fetched here. A commit is pinned only if a key you trust signed it and it is on the line of main that you trusted.

5. **See what changes.** The commits between the old pin and the new one, so that you know what the hosts will get. Pinning an older commit is asked about first. The hosts are evaluated as they are now, to compare with after.

6. **Pin it.** Locks fleet-manager at the new commit in flake.lock, and nothing else. If the run stops before the commit, flake.lock is put back.

7. **Check every host still evaluates.** A new fleet-manager may break a host. Each host is evaluated again, and the ones whose system changes are listed.

8. **Commit the pin.** One commit that changes flake.lock only. The repository's own git settings sign it.

9. **Push it.** Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first. What you push is the commit you trust on main from then on.

10. **Next.** The pin is on main only. Each host fetches its own branch, so it gets the new fleet-manager once you publish.

   > Hosts fetch their own branches, not main: publish them now, and each host takes the new fleet-manager at its next sync:
   > fleet inventory publish --flake FLAKE_GIVEN
   > A host can be told to sync at once with: ssh support@\<host> sudo systemctl start config-auto-update

It needs `git`, `nix` on the PATH; `fleet doctor` says where to get them.
