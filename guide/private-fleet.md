# A private fleet

A real fleet keeps its hosts and users in a private repository, the inventory, which pins fleet-manager and builds them with `lib.mkFleet`.

## Setting one up

The hosts in `hosts/` here are examples and test hosts, and they keep working
as they are. A real fleet can keep its hosts and users in a private repository
instead. That repository pins this one and builds its hosts with
`lib.mkFleet`:

```nix
{
  inputs.fleet-manager.url = "git+https://github.com/nnishant776/fleet-manager";
  outputs = { self, fleet-manager }: fleet-manager.lib.mkFleet {
    inherit self;
    hosts = ./config/hosts;             # one folder per host, as hosts/ here
    users = ./config/users;             # optional, as users/ here
    extraSystems = [ "x86_64-linux" ];  # the apps where you and CI run them
    hostBranches = true;                # each host fetches only its own files (below)
  };
}
```

Its machines deploy from the private repository: each records that
repository's commit as its revision, verifies it against the signers file its
configuration names, and builds with the fleet-manager its `flake.lock` pins.
Moving to a newer fleet-manager is a signed commit there that updates the
lock. A host's folder works the same there; only the path to a signers file
changes with the depth, as in `../../../fleet/allowed_signers`.

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
`fleet host sync <host>`; `fleet inventory publish --flake <checkout> --sync`
publishes and then syncs every host whose branch changed.

## Moving to a newer fleet-manager

The inventory pins one
commit of fleet-manager in its `flake.lock`. To move it, run
`fleet inventory pin --flake inventory` (the name of the clone, or a checkout),
then publish as above. It takes fleet-manager's newest main, or the commit
you give with `--to`, only if a key you trust signed it. It shows the commits
the pin moves over, locks the new commit, and checks that every host still
evaluates. Then it commits `flake.lock`, signed, and pushes it once you
approve. Hosts take the new fleet-manager when you publish.

## Installing a host from it

Run `fleet install prep <host> --flake
<checkout of it>` on your machine, and the line it prints on the installer
(see [First run](../README.md#first-run)). It sets
`FLAKE=git+ssh://git@github.com/example-org/inventory?ref=host/<host>&rev=<commit>`,
and `os-install` installs exactly that commit of the host's branch. With
host branches, prep also checks that the branch was published from main's
newest commit. `os-install --flake git+ssh://…?ref=…&rev=<commit> -H <host>`
does the same by hand. The host can't read the
repository yet, so the identity comes first. The install shows the
enrollment file and waits while you enroll the host and register both of its
keys. Then it clones the private flake with the configuration key and
installs. The diagram under
[Installing a machine](installing.md) shows what moves where.

## Bringing in a running NixOS machine

Add its folder
there, push, publish its branch, then run `fleet host enroll <host> --flake
<checkout of it>`. For a machine that does not run a fleet configuration
yet, enroll does this in order:
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
It ends by listing the commands that finish the move: `fleet host enroll`,
`fleet host check`, and removing the host from fleet-manager.

## Moving a running host to it

1. Copy the host's folder there, with `repo.url` set to the private
   repository's SSH address, and push. With host branches, publish its
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
   host accepted from there, so the sync goes on from it. Nothing in this
   repository names the private one; the host learns it from the
   configuration it switches to.
3. Delete the host's folder here.
