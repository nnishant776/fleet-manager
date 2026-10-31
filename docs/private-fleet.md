# 9. A private fleet

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 8. Secrets](secrets.md) · [Contents](README.md) · [10. The fleet tool →](fleet-tool.md)
<!-- /book:top -->

How a fleet keeps its hosts and users in a private repository, the inventory,
and works with it from day to day: setting one up, the key a host reads it
with, host branches, moving to a newer fleet-manager, installing hosts and
bringing running machines in. It builds on
[Signing commits and trusting them](signing-and-trust.md) and
[Secrets](secrets.md).

<!-- book:sections -->
In this chapter:

- [Setting one up](#setting-one-up)
- [The configuration key](#the-configuration-key)
- [Each host gets only its own files](#each-host-gets-only-its-own-files)
- [What a host fetches](#what-a-host-fetches)
- [Its own CI](#its-own-ci)
- [Moving to a newer fleet-manager](#moving-to-a-newer-fleet-manager)
- [Installing a host from it](#installing-a-host-from-it)
- [Bringing in a running NixOS machine](#bringing-in-a-running-nixos-machine)
- [Moving a host fleet-manager defines](#moving-a-host-fleet-manager-defines)
- [Moving a running host to it](#moving-a-running-host-to-it)
<!-- /book:sections -->

## Setting one up

The hosts in fleet-manager's `hosts/` are examples and test hosts, and they
keep working as they are. A real fleet can keep its hosts and users in a
private repository instead. That repository pins fleet-manager and builds its
hosts with `lib.mkFleet`:

```nix
{
  inputs.fleet-manager.url = "git+https://github.com/nnishant776/fleet-manager";
  outputs = { self, fleet-manager }: fleet-manager.lib.mkFleet {
    inherit self;
    hosts = ./config/hosts;             # one folder per host, as hosts/ in fleet-manager
    users = ./config/users;             # optional, as users/ in fleet-manager
    extraSystems = [ "x86_64-linux" ];  # the apps where you and CI run them
    hostBranches = true;                # each host fetches only its own files (below)
  };
}
```

The inventory then looks like this:

```
flake.nix               pins fleet-manager, and builds config/ with lib.mkFleet
flake.lock              the exact fleet-manager, and so nixpkgs, the hosts build with
config/hosts/<host>/    one folder per machine
config/users/<user>/    optional per-user configuration (configuration only)
fleet/allowed_signers   the keys whose signed commits the hosts accept
```

Its machines deploy from the private repository: each records that
repository's commit as its revision, verifies it against the signers file its
configuration names, and builds with the fleet-manager its `flake.lock` pins.
Moving to a newer fleet-manager is a signed commit there that updates the
lock. A host's folder works the same there; only the path to a signers file
changes with the depth, as in `../../../fleet/allowed_signers`.

Passwords and keys are not kept in the inventory; they are in the
secrets repository (see [Secrets](secrets.md)). On a host with secrets, SSH login and sudo
keys come from there only, so the only key material in the inventory is
`fleet/allowed_signers`.

Your signature on that commit vouches for the fleet-manager it pins, so pin a
commit whose signature you have checked, by its hash. Check it in a
fleet-manager checkout of your own, set up by `fleet setup` to check
signatures:

```bash
git -C <fleet-manager checkout> fetch origin
git -C <fleet-manager checkout> verify-commit origin/main
nix flake lock --override-input fleet-manager \
  "git+https://github.com/nnishant776/fleet-manager?rev=$(git -C <fleet-manager checkout> rev-parse origin/main)"
```

The input is `git+https://`, not `github:`. With git, the files Nix gets are
the ones the commit's hash names. A `github:` input is a tarball that GitHub
makes, and nothing checks it against the commit.

Give the tools a clone of the inventory of their own, with `fleet repo add`
([The tools' own clones](signing-and-trust.md#the-tools-own-clones) in *Signing commits and trusting them*). The
commands below then take its name for `--flake`, such as `--flake inventory`;
a path to a checkout works as well.

## The configuration key

A private repository needs a key to read. GitHub allows a deploy key on one
repository only, and each host's deploy key is on the secrets repository, so
a host has a second key for its configuration: `config_deploy_key`, next
to `secrets_deploy_key` for the secrets. The host
makes it with the rest of its identity, and its enrollment file lists it as
`config_deploy_pubkey`. A host fetches with it whenever its configuration
names an SSH address; an `https://` one is fetched with no key:

```nix
conf.fleet.repo.url = "git@github.com:example-org/inventory.git";
```

The key has to be a read-only deploy key of the private repository.
`fleet host enroll <host> --flake <checkout>` registers it with `gh`.

## Each host gets only its own files

Add `hostBranches = true;` to the
`lib.mkFleet` call above, and each host tracks a branch of its own,
`host/<name>`, instead of `main`. The branch holds:

- `flake.nix`, `flake.lock` and `fleet/`;
- the host's folder and the folders of its users;
- the paths listed in `shared`, such as modules the hosts share.

Nothing of the other hosts is on it. Hosts, the installer and enroll fetch
that one branch, so the other hosts' definitions never reach a host's
checkout or its Nix store. `main` stays the full inventory: you edit there,
and CI checks it. After pushing a change to `main`, run this on your machine:

```bash
fleet inventory publish --flake <checkout>
```

It checks that main's newest commit is signed by a key you trust, follows the
commit you trusted last on main, and is pushed.
It cuts each host's branch from that commit without touching the checkout, and
checks that each host builds from its branch alone. Then it adds a signed
commit to every branch whose files changed, and pushes them once you approve.
A host that needs a file outside its folder fails that check; list the file in
`shared`. Each branch has a history of its own, so a host's protection against
older commits works as before. One limit remains: root on a host can still use
its configuration key to fetch the other branches, since GitHub's deploy keys
cannot be limited to a branch.

To sync a host right away, instead of at its next sync, run
`fleet host sync <host>`: it starts the host's sync, waits, and shows what the
host runs, its secrets and its health.
`fleet inventory publish --flake <checkout> --sync` publishes and then syncs
every host whose branch changed, one after another, and checks that each runs
the commit just pushed.

## What a host fetches

Its own branch of the inventory, `host/<host>`, and its own branch `host/<host>`
in the secrets repository, plus the branches `user/<u>` there of the users its
secrets file lists. It verifies the newest commit of each against
`fleet/allowed_signers` of the configuration it runs, and a user's branch also
against that user's own key. It builds with the fleet-manager that
`flake.lock` pins, by its hash.

## Its own CI

fleet-manager's CI never sees an inventory's hosts: it tests the `fleet` tool,
and its weekly flake update evaluates only fleet-manager's own example and test
hosts. So the inventory needs a check of its own:
`nix run .#check-purity` evaluates every host, and CI should run it on every
push and pull request.

## Moving to a newer fleet-manager

The inventory pins one
commit of fleet-manager in its `flake.lock`. To move it, run
`fleet inventory pin --flake inventory` (the name of the clone, or a checkout),
then publish as above. It takes fleet-manager's newest main, or the commit
you give with `--to`, only if a key you trust signed it. It shows the commits
the pin moves over, and pins only if each is signed by a key you trust. It
evaluates every host before and after, and names the hosts whose system
changes. Then it commits `flake.lock`, signed, and pushes it once you
approve. Hosts take the new fleet-manager when you publish, not with the
commit on main. `publish`, `install prep` and `host enroll` check the pin the
same way, so an unsigned or older commit is never pinned by mistake.

## Installing a host from it

Run `fleet install prep <host> --flake <checkout of it>` on your machine, and
the line it prints on the installer
(see [First run](../README.md#first-run) in the README). It sets
`FLAKE=git+ssh://git@github.com/example-org/inventory?ref=host/<host>&rev=<commit>`,
and `os-install` installs exactly that commit of the host's branch. With
host branches, prep also checks that the branch was published from main's
newest commit. `os-install --flake git+ssh://…?ref=…&rev=<commit> -H <host>`
does the same by hand. An install of a private host without the pins is
refused. The diagram under
[Enrolling during the install](installing.md#enrolling-during-the-install) in *Installing a machine* shows what
moves where.

The host can't read the repository before it is enrolled, so the install does
things in this order:

1. It makes the host's identity, in memory, and shows the enrollment file and
   a QR code of it.
2. On your machine, in the secrets repository, set up what the host needs
   before it boots:
   - `fleet secrets host-enroll <host> --from <the file>` (does not push);
   - support's keys, `fleet secrets user-keys support <host>
     --authorized-keys support.keys` (pushes). Without them the host has no
     way in until they are added and it syncs;
   - each user's password, `fleet secrets user-password <user> <host>` (does
     not push);
   - then push the host's branch: `git push origin host/<host>`.

   Register the host's deploy key read-only on the secrets repository, and its
   configuration key read-only on the inventory.
3. Back at the machine, press Enter. It clones the inventory with the new
   key and installs. The identity goes onto the new disk.

The whole flow, step by step, is under
[Onboarding a machine](secrets-runbooks.md#onboarding-a-machine) in *Secrets runbooks*.

The Raspberry Pi card (`rpi-image`) does not set a private host up by itself
yet. Bring a Pi in once it runs, as in the next section.

## Bringing in a running NixOS machine

Add its folder to the inventory, push, publish its branch, then run
`fleet host enroll <host> --flake <checkout of it>`. For a machine that does
not run a fleet configuration yet, enroll does this in order:

1. Checks the commit to install, as `fleet install prep` does.
2. Makes the host's identity with `fleet-secrets-sync` from the checked
   fleet-manager commit.
3. Enrolls the host and registers both of its keys.
4. Checks the configuration fits the machine as it is.
5. Checks that the host's secrets branch holds every secret the
   configuration declares, reading the sops files without decrypting them,
   and stops before the switch, with the command that adds what is missing.
6. Shows what `/etc/nixos` holds, warns that all of it is deleted after the
   switch, and asks. If you say no, nothing changes.
7. Switches, fetching the private configuration with the host's own key.
8. Replaces `/etc/nixos` with the fleet's checkout, made by the first-boot
   clone at the commit the host now runs. This runs as a unit on the host,
   so a dropped connection cannot leave the host without a checkout.
9. Syncs the host and runs `fleet host check` (`--sync=false` leaves this out).

The configuration has to describe the machine as it is, since nothing is
reinstalled:

- `conf.hardware.disk.enable = false` and the machine's own
  `hardware-configuration.nix`, from the machine. Copy it into the host's
  folder before you enroll, since `/etc/nixos` is deleted after the switch;
  `nixos-generate-config --show-hardware-config` prints it again. enroll
  refuses when what the configuration mounts at `/` or `/boot` is not what
  the machine has.
- The boot loader the machine uses. enroll refuses a UEFI boot loader on a
  machine without UEFI.
- Every account to keep, declared, with its password set in the secrets
  repository before the switch. Accounts become fixed by the configuration,
  and an undeclared one is removed. Keep one admin account with SSH-key
  login, so a mistake cannot lock you out.

## Moving a host fleet-manager defines

A host that fleet-manager defines already moves into an inventory with
`fleet inventory import <host> --flake <inventory>`. It copies the host's
folder and its users' folders from a commit of fleet-manager, changes only
the path to `fleet/allowed_signers` and the address the host fetches from,
checks the host in the inventory, commits, pushes and publishes its branch.
It refuses a host without secrets, and says what to set in fleet-manager
first. It ends by listing the commands that finish the move:
`fleet host enroll`, `fleet host check`, and removing the host from
fleet-manager.

## Moving a running host to it

1. Copy the host's folder to the inventory, with `repo.url` set to the
   inventory's SSH address, and push. With host branches, publish its
   branch too: `fleet inventory publish <host> --flake <checkout of it>`.
2. Run `fleet host enroll <host> --flake <checkout of it>`. It sees that
   the host runs a configuration from another repository, and:
   1. adds the configuration key on the host and registers it;
   2. checks the configuration fits the machine, and that the host's secrets
      branch holds every secret it declares;
   3. warns that `/etc/nixos` is replaced, and asks;
   4. switches the host to its configuration there, on its branch, fetched
      with that key;
   5. replaces `/etc/nixos` with a checkout of the new repository;
   6. syncs the host and runs `fleet host check`, unless `--sync=false`.

   The first-boot clone records the commit it checks out as the first the
   host accepted from there, so the sync goes on from it. Nothing in
   fleet-manager names the private repository; the host learns it from the
   configuration it switches to.
3. Delete the host's folder from the repository it came from.

<!-- book:bottom -->
---

[← 8. Secrets](secrets.md) · [Contents](README.md) · [10. The fleet tool →](fleet-tool.md)
<!-- /book:bottom -->
