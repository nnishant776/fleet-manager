# Appendix C. Glossary

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← Appendix B. Things to know](things-to-know.md) · [Contents](README.md) · [Appendix D. Further reading →](further-reading.md)
<!-- /book:top -->

The fleet's own terms, each defined in a sentence or two. The link after each
definition goes to where it is explained. The [Index](index.md) lists every
place a term is mentioned.

<!-- book:sections -->
In this chapter: [A](#a) · [B](#b) · [C](#c) · [D](#d) · [E](#e) · [F](#f) · [H](#h) · [I](#i) · [L](#l) · [M](#m) · [O](#o) · [P](#p) · [R](#r) · [S](#s) · [T](#t) · [U](#u)
<!-- /book:sections -->

## A

- **accepted commit**: The last commit a host took from a branch, recorded
  on the host. A new commit is taken only if it is that one or follows it.
  See [What a NixOS host does at each sync](how-it-works.md#what-a-nixos-host-does-at-each-sync).
- **activation**: Switching a machine to a built system. sops-nix decrypts the
  secrets into `/run/secrets` at activation. See
  [On the host](secrets.md#on-the-host).
- **age**: The encryption tool behind the secrets. Each host, each user with a
  branch of their own, and the maintainer have an age key. See
  [Trust model](secrets.md#trust-model).
- **`allowed_signers`**: The file of SSH keys whose signed commits a fleet
  accepts, `fleet/allowed_signers` in the configuration. See
  [The signers file](signing-and-trust.md#the-signers-file).

## B

- **`bootstrap`**: The script a first run fetches. It installs Nix where it is
  missing, then hands over to `fleet install`, `system-switch` or nix-darwin.
  See [First run](../README.md#first-run).

## C

- **`check-purity`**: The app that checks every system evaluates without
  `--impure`. See [The flake](things-to-know.md#the-flake).
- **`conf.*`**: The fleet's vocabulary of options, which every host sets and
  the modules implement. See
  [From `conf.*` to a system](how-it-works.md#from-conf-to-a-system).
- **configuration**: The flake a host builds, either fleet-manager itself or a
  private inventory. See [The pieces](how-it-works.md#the-pieces).
- **configuration deploy key**: A host's read-only key for a private
  configuration, `config_deploy_key`. See
  [The configuration key](private-fleet.md#the-configuration-key).
- **`config-auto-update`**: The systemd service that runs the sync, on a
  timer. See
  [What a NixOS host does at each sync](how-it-works.md#what-a-nixos-host-does-at-each-sync).

## D

- **deploy key**: An SSH key registered on one repository, read-only here. Each
  host has one for the secrets and one for a private configuration. See
  [Trust model](secrets.md#trust-model).
- **`disk-extend`**: The command that adds a disk to a running machine's
  volume group. See
  [Adding a disk to a running machine](installing.md#adding-a-disk-to-a-running-machine).

## E

- **enrollment**: Admitting a host or a user to the secrets, from their
  enrollment file. See [Onboarding a machine](secrets-runbooks.md#onboarding-a-machine).
- **enrollment file**: The public half of a host's or a user's identity: its
  age recipient and its public keys. Its integrity matters, not its secrecy.
  See [Trust model](secrets.md#trust-model).

## F

- **first-boot clone**: `fleet-bootstrap`, which clones the configuration into
  `/etc/nixos` at the first boot, at the commit the system was built from. See
  [What the install does](installing.md#what-the-install-does).
- **`fleet`**: The guided tool that walks through the fleet's procedures, on
  the maintainer's machine. See [The fleet tool](fleet-tool.md).
- **fleet host**: A machine with `conf.fleet.repo.url`, which syncs from its
  configuration. See
  [What a NixOS host does at each sync](how-it-works.md#what-a-nixos-host-does-at-each-sync).
- **`fleet-identity`**: The host's command that prints its enrollment file, or
  exports its identity for a reinstall. See [The tools](secrets.md#the-tools).
- **`fleet-secrets`**: The maintainer's shell tool for the secrets repository;
  `fleet secrets …` does the same and more. See
  [The tools](secrets.md#the-tools).
- **`fleet-secrets-sync`**: The host's program that fetches, verifies and
  snapshots its secrets. See [On the host](secrets.md#on-the-host).
- **`fleet-sync-now`**: Starts the sync at once, for a privileged account at the
  machine. See [Things to know about the sync](how-it-works.md#things-to-know-about-the-sync).
- **`fleet-sync-status`**: Prints why the last sync stopped, if it did. See
  [Things to know about the sync](how-it-works.md#things-to-know-about-the-sync).
- **`fleet-user-secret`**: Prints one of the secrets a user keeps in their own
  file. See [Onboarding a user](secrets-runbooks.md#onboarding-a-user).
- **forge**: The service the repositories live on, GitHub here. See
  [The pieces](how-it-works.md#the-pieces).

## H

- **`home-switch`**: Activates the home of a user with a self-managed home. See
  [Switching a home configuration](../README.md#switching-a-home-configuration).
- **host branch**: `host/<name>`, the branch a host fetches, in the secrets
  repository and in an inventory with host branches. See
  [Each host gets only its own files](private-fleet.md#each-host-gets-only-its-own-files).
- **host identity**: A host's three keys, made on the host and never sent
  anywhere: its age key and two deploy keys. See
  [On the host](secrets.md#on-the-host).
- **host secret**: A secret in `hosts/<host>.yaml` that a module of the host
  declares. See [Host secrets](secrets.md#host-secrets).
- **hotspot**: A Wi-Fi network a machine offers to share its connection. See
  [Hotspots](networking.md#hotspots).

## I

- **inventory**: A private repository that holds a fleet's hosts and users,
  pins fleet-manager and builds them with `lib.mkFleet`. See
  [A private fleet](private-fleet.md).

## L

- **`lib.mkFleet`**: The function that builds a fleet's systems, homes and apps
  from its host folders. See [Setting one up](private-fleet.md#setting-one-up).

## M

- **machine type**: What a machine physically is, `conf.machineType`: laptop,
  desktop, headless or vm. See [Machine types](roles.md#machine-types).
- **maintainer**: Whoever holds the fleet's signing key and recovery identity.
  See [The pieces](how-it-works.md#the-pieces).

## O

- **`os-install`**: The installer: it partitions, installs and makes the host's
  identity. See [With os-install](installing.md#with-os-install).

## P

- **personal machine**: A host handed out to one person, who makes their own
  account at the first boot. See
  [Personal machines](accounts.md#personal-machines).
- **pin**: The commit of fleet-manager an inventory's `flake.lock` names. See
  [Moving to a newer fleet-manager](private-fleet.md#moving-to-a-newer-fleet-manager).

## R

- **recovery identity**: The maintainer's age key, kept encrypted with a
  passphrase, that can decrypt and re-encrypt every file. See
  [Trust model](secrets.md#trust-model).
- **recovery recipient**: The public half of the recovery identity. Every file
  is encrypted to it. See [Trust model](secrets.md#trust-model).
- **role**: What a machine is for, `conf.role`, which sets its defaults. See
  [Roles](roles.md#roles).
- **rollback on failure**: Going back to the previous generation when a new
  system fails to activate. See
  [What a NixOS host does at each sync](how-it-works.md#what-a-nixos-host-does-at-each-sync).
- **runbook**: A page of one `fleet` command, written from the command itself.
  See [Runbooks](fleet-tool.md#runbooks).

## S

- **secrets repository**: The private repository of sops ciphertext, one
  branch per host. See [The secrets repository](secrets.md#the-secrets-repository).
- **secrets snapshot**: The host's copy of its own files from the secrets
  repository, which activation decrypts. See [On the host](secrets.md#on-the-host).
- **self-managed home**: A home its user activates, with `selfManagedHome`,
  instead of the system. See [Things to know](accounts.md#things-to-know).
- **signing key**: The SSH key that signs commits. See
  [Signing your commits](signing-and-trust.md#signing-your-commits).
- **sops**: The tool that encrypts the values of a YAML file. See
  [The secrets repository](secrets.md#the-secrets-repository).
- **sops-nix**: The NixOS module that decrypts sops files at activation. See
  [On the host](secrets.md#on-the-host).
- **support account**: `support`, the maintainer's account on a fleet host,
  logged in to with keys only. See
  [Onboarding a machine](secrets-runbooks.md#onboarding-a-machine).
- **sync**: A host's periodic fetch, check, build and activation of its
  configuration and secrets. See
  [What a NixOS host does at each sync](how-it-works.md#what-a-nixos-host-does-at-each-sync).
- **`system-switch`**: Builds and switches a host from a flake. See
  [Building and switching a system](../README.md#building-and-switching-a-system).

## T

- **tools' own clones**: The clones `fleet repo add` keeps for the tools
  alone, fetched and checked before each use. See
  [The tools' own clones](signing-and-trust.md#the-tools-own-clones).
- **trusted commit**: On the maintainer's machine, the last commit the tools
  accepted on a branch. It is what the accepted commit is on a host. See
  [Your own copy of the trusted commits](signing-and-trust.md#your-own-copy-of-the-trusted-commits).

## U

- **user branch**: `user/<name>`, a user's own branch in the secrets
  repository, which the hosts that list them read. See
  [Users with a branch of their own](secrets.md#users-with-a-branch-of-their-own).
- **user identity**: A user's own age key and SSH signing key, made by
  `fleet user init`. See [Trust model](secrets.md#trust-model).

<!-- book:bottom -->
---

[← Appendix B. Things to know](things-to-know.md) · [Contents](README.md) · [Appendix D. Further reading →](further-reading.md)
<!-- /book:bottom -->
