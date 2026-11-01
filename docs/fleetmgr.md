# 10. The fleetmgr tool

<!-- book:top — written by `fleetmgr docs` from SUMMARY.md and the headings; do not edit -->
[← 9. A private fleet](private-fleet.md) · [Contents](README.md) · [11. Secrets runbooks →](secrets-runbooks.md)
<!-- /book:top -->

`fleetmgr` walks through the fleet's procedures one step at a time: installing a
machine, enrolling a host, secrets, the support key, and checks. Each step says
what it does and why, shows the exact command, asks before it changes
anything, and checks the result. `fleetmgr` alone shows a menu, and
`fleetmgr <command> --help` explains a command.

This chapter covers installing it, running it without prompts, what a run
that stops leaves behind, and how the installer gets it. Each command has a
page of its own in the [runbooks](../tools/fleetmgr/runbooks/README.md).

<!-- book:sections -->
In this chapter:

- [Installing it](#installing-it)
- [Without prompts](#without-prompts)
- [A run that stops](#a-run-that-stops)
- [Runbooks](#runbooks)
- [On the installer](#on-the-installer)
- [fleetmgr and fleetmgr-secrets](#fleetmgr-and-fleetmgr-secrets)
- [The old name](#the-old-name)
<!-- /book:sections -->

## Installing it

It is not installed anywhere by default.
Install it yourself where you want it:

```bash
nix profile install /etc/nixos#fleetmgr     # on a NixOS machine of the fleet
nix profile install .#fleetmgr              # or from a checkout you have checked
nix run .#fleetmgr -- host check pi1   # once, without installing
nix profile upgrade fleetmgr                # later, for a newer version
```

Install it from a checkout, not with `github:` straight from main. It runs
with your agent and, for secrets, your recovery key, so it should be code
whose signature has been checked. `/etc/nixos` on a NixOS machine of the fleet
is such a checkout: its sync checks every commit before checking it out. In a
checkout of your own, run `git verify-commit HEAD` first. For the same reason,
fleetmgr reads hosts only from `--flake`, this directory's flake or `/etc/nixos`,
and asks for `--flake` when there is none.

## Without prompts

Every question has a flag, so a command also runs without prompts. `--dry-run`
shows every step and command for a real host, and runs only the ones that
look; `--yes` makes the changes without asking.

## A run that stops

A run that does not finish, because of Ctrl+C, a failed step or a "no", puts
back what it changed on this machine, such as half-made changes in the secrets
repository, and says what stays: commits already pushed, keys it made, a host's
identity, or a sync still running on the host. Steps that are done are skipped,
so the same command, run again, goes on from there.

## Runbooks

Each command has a page in [`tools/fleetmgr/runbooks`](../tools/fleetmgr/runbooks/README.md)
with its flags and its steps. `fleetmgr docs` writes the pages from the commands
themselves, and the tests fail when they are out of date.

## On the installer

On the NixOS installer, `bootstrap` runs `fleetmgr install` (see [First run](../README.md#first-run) in the README). For that, every push to `main` that changes the tool
publishes static binaries for Linux and macOS as the latest release, with
`SHA256SUMS` and an attestation for each; `gh attestation verify <file> --repo
nnishant776/fleet-manager` checks where a binary came from. `.#fleetmgr-static` builds
it with Nix instead. With `REV`, as the line from `fleetmgr install prep` gives
it, `bootstrap` builds `fleetmgr` from that commit rather than taking the latest
release, which is whatever `main` had when CI last ran.

## fleetmgr and fleetmgr-secrets

`fleetmgr secrets …` does what `fleetmgr-secrets` does, with the same files and
commit messages. `nix run .#fleetmgr-secrets` is still the shell version. Three
commands are only in `fleetmgr`: `fleetmgr secrets user-keys`, which stores a user's login
and sudo keys and pushes the branch (see [Secrets](secrets.md)), and
`fleetmgr secrets user-enroll` and `user-retire`. So are the
commands that a user with a branch of their own runs for themselves: `fleetmgr user
init`, `password`, `keys` and `secret set|unset`. On a host whose
configuration sets `fleet.support.keysFromSecrets`, `fleetmgr support-key rotate`
changes support's keys there instead of asking you to edit the configuration; and
`fleetmgr host enroll` checks, before it switches the host, that its branch holds every
secret the configuration declares, reading the sops files without decrypting them.

## The old name

Until November 2026 the tool was called `fleet`, and its shell twin
`fleet-secrets`. The programs on hosts were `fleet-sync-now`,
`fleet-secrets-sync` and the rest. It was renamed because nixpkgs already has
`fleet` and `fleetctl`, the server and admin tool of FleetDM, a
device-management product that a fleet like this one might also run.

Every command is the same with the new name: `fleet host check` is now
`fleetmgr host check`. To move over, remove the old install and install the new
one:

```bash
nix profile remove fleet
nix profile install /etc/nixos#fleetmgr
```

Also rename any `FLEET_*` variable you set, such as `FLEET_THEME` or
`FLEET_SECRETS_REPO`, to `FLEETMGR_*`.

Some things did not change. The tool's state stays in `~/.local/state/fleet` and
`~/.local/share/fleet`, so your trusted keys, trusted commits and the tools' own
clones carry over. So does the `gpg.ssh.allowedSignersFile` setting in your
checkouts, which points there. On hosts, `/var/lib/fleet` and
`/var/lib/fleet-sync` stay too. The configuration still says `conf.fleet`, since
it is about the fleet, not the tool.

A host gets the new program names when its inventory pins a fleet-manager commit
that has them; until then it still has the old ones. Move the pin before you
enroll a new host with `fleetmgr host enroll`.

<!-- book:bottom -->
---

[← 9. A private fleet](private-fleet.md) · [Contents](README.md) · [11. Secrets runbooks →](secrets-runbooks.md)
<!-- /book:bottom -->
