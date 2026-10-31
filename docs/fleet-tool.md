# 10. The fleet tool

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 9. A private fleet](private-fleet.md) · [Contents](README.md) · [11. Secrets runbooks →](secrets-runbooks.md)
<!-- /book:top -->

`fleet` walks through the fleet's procedures one step at a time: installing a
machine, enrolling a host, secrets, the support key, and checks. Each step says
what it does and why, shows the exact command, asks before it changes
anything, and checks the result. `fleet` alone shows a menu, and
`fleet <command> --help` explains a command.

This chapter covers installing it, running it without prompts, what a run
that stops leaves behind, and how the installer gets it. Each command has a
page of its own in the [runbooks](../tools/fleet/runbooks/README.md).

<!-- book:sections -->
In this chapter:

- [Installing it](#installing-it)
- [Without prompts](#without-prompts)
- [A run that stops](#a-run-that-stops)
- [Runbooks](#runbooks)
- [On the installer](#on-the-installer)
- [fleet and fleet-secrets](#fleet-and-fleet-secrets)
<!-- /book:sections -->

## Installing it

It is not installed anywhere by default.
Install it yourself where you want it:

```bash
nix profile install /etc/nixos#fleet        # on a NixOS machine of the fleet
nix profile install .#fleet                 # or from a checkout you have checked
nix run .#fleet -- host check pi1   # once, without installing
nix profile upgrade fleet                   # later, for a newer version
```

Install it from a checkout, not with `github:` straight from main. It runs
with your agent and, for secrets, your recovery key, so it should be code
whose signature has been checked. `/etc/nixos` on a NixOS machine of the fleet
is such a checkout: its sync checks every commit before checking it out. In a
checkout of your own, run `git verify-commit HEAD` first. For the same reason,
fleet reads hosts only from `--flake`, this directory's flake or `/etc/nixos`,
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

Each command has a page in [`tools/fleet/runbooks`](../tools/fleet/runbooks/README.md)
with its flags and its steps. `fleet docs` writes the pages from the commands
themselves, and the tests fail when they are out of date.

## On the installer

On the NixOS installer, `bootstrap` runs `fleet install` (see [First run](../README.md#first-run) in the README). For that, every push to `main` that changes the tool
publishes static binaries for Linux and macOS as the latest release, with
`SHA256SUMS` and an attestation for each; `gh attestation verify <file> --repo
nnishant776/fleet-manager` checks where a binary came from. `.#fleet-static` builds
it with Nix instead. With `REV`, as the line from `fleet install prep` gives
it, `bootstrap` builds `fleet` from that commit rather than taking the latest
release, which is whatever `main` had when CI last ran.

## fleet and fleet-secrets

`fleet secrets …` does what `fleet-secrets` does, with the same files and
commit messages. `nix run .#fleet-secrets` is still the shell version. Three
commands are only in `fleet`: `fleet secrets user-keys`, which stores a user's login
and sudo keys and pushes the branch (see [Secrets](secrets.md)), and
`fleet secrets user-enroll` and `user-retire`. So are the
commands that a user with a branch of their own runs for themselves: `fleet user
init`, `password`, `keys` and `secret set|unset`. On a host whose
configuration sets `fleet.support.keysFromSecrets`, `fleet support-key rotate`
changes support's keys there instead of asking you to edit the configuration; and
`fleet host enroll` checks, before it switches the host, that its branch holds every
secret the configuration declares, reading the sops files without decrypting them.

<!-- book:bottom -->
---

[← 9. A private fleet](private-fleet.md) · [Contents](README.md) · [11. Secrets runbooks →](secrets-runbooks.md)
<!-- /book:bottom -->
