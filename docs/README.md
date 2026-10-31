# fleet-manager: the guide

This book is about fleet-manager. It covers how one set of modules builds NixOS
and macOS machines, and how a fleet of them keeps itself up to date from signed
commits and gets its secrets. It also covers how to run such a fleet. The
[README](../README.md) has the first steps: a first run, adding a machine, and
building and switching. Start there if you have not built a machine yet.

## Who it is for

- **Anyone writing a host's configuration** needs Part II and the
  [Configuration reference](configuration.md) in Appendix A.
- **The maintainer of a fleet**, who holds its signing and recovery keys, needs
  all of it, and Part III most.
- **Anyone curious how it works** can read Part I alone. It is the map of the
  rest.

## How it is arranged

- **Part I, How it fits together**, is one chapter. It covers the pieces of a
  fleet, how a host's configuration becomes a system, and what a host does at
  each sync.
- **Part II, Machines**, covers installing a machine, building a Raspberry Pi
  image, and the defaults roles and machine types set. It then covers accounts
  and homes, and networking.
- **Part III, Running a fleet**, covers signed commits and trust, secrets, a
  private fleet of your own, and the `fleet` tool. It ends with the secrets
  runbooks, step by step.
- **The appendices** hold the configuration reference, things to know, a
  glossary, further reading and an index.

Read Part I first. After that, read in order, or go straight to a chapter:
each one says at its start what it covers and what it builds on. Every chapter
begins with links to the chapters before and after it, the contents, and a
list of its sections. Links into another chapter name it, as in
"[Trust model](secrets.md#trust-model) in *Secrets*". Each `fleet` command
also has a page of its own in the [runbooks](../tools/fleet/runbooks/README.md),
written from the command itself.

## Conventions

- **Example names.** The hosts are `laptop1`, `server1` and `my-laptop`, and
  the users are `alice`, `bob` and `admin`. The forge organisation is
  `example-org`, with the repositories `inventory` and `secrets`. `support`
  is the maintainer's account on fleet hosts. None of them names a real
  machine or person.
- **Placeholders** in angle brackets, such as `<host>` or
  `<configuration checkout>`, are yours to fill in. The runbooks write them in
  capitals, such as `HOST`.
- **Where a command runs.** Commands run on the maintainer's machine, in a
  checkout of the repository the text names, unless the text says "on the
  host" or "at the installer". Commands that start with `sudo` run on the host.
- **The configuration** is the flake a host builds: this repository, or a
  private inventory that pins it.

## Contents

<!-- book:contents -->
### Part I. How it fits together

- [1. How it works](how-it-works.md)
  - [The pieces](how-it-works.md#the-pieces)
  - [From `conf.*` to a system](how-it-works.md#from-conf-to-a-system)
  - [A fleet host up close](how-it-works.md#a-fleet-host-up-close)
  - [Every flow at a glance](how-it-works.md#every-flow-at-a-glance)
  - [A change reaches a host](how-it-works.md#a-change-reaches-a-host)
  - [What a NixOS host does at each sync](how-it-works.md#what-a-nixos-host-does-at-each-sync)
  - [Secrets reach a host](how-it-works.md#secrets-reach-a-host)
  - [Users who keep their own secrets](how-it-works.md#users-who-keep-their-own-secrets)
  - [A new machine](how-it-works.md#a-new-machine)
  - [Bringing in a running machine](how-it-works.md#bringing-in-a-running-machine)
  - [Personal machines and adoption](how-it-works.md#personal-machines-and-adoption)
  - [The maintainer's machine](how-it-works.md#the-maintainers-machine)
  - [Reaching a host](how-it-works.md#reaching-a-host)
  - [A newer fleet-manager, CI and releases](how-it-works.md#a-newer-fleet-manager-ci-and-releases)
  - [Homes](how-it-works.md#homes)
  - [Logs and audit](how-it-works.md#logs-and-audit)
  - [Code that reaches machines in other ways](how-it-works.md#code-that-reaches-machines-in-other-ways)
  - [macOS and the Raspberry Pi](how-it-works.md#macos-and-the-raspberry-pi)
  - [Things to know about the sync](how-it-works.md#things-to-know-about-the-sync)
  - [What it is built on](how-it-works.md#what-it-is-built-on)

### Part II. Machines

- [2. Installing a machine](installing.md)
  - [From checked commits](installing.md#from-checked-commits)
  - [On a Mac](installing.md#on-a-mac)
  - [Without bootstrap](installing.md#without-bootstrap)
  - [With os-install](installing.md#with-os-install)
  - [Adding a disk to a running machine](installing.md#adding-a-disk-to-a-running-machine)
  - [What the install does](installing.md#what-the-install-does)
  - [Enrolling during the install](installing.md#enrolling-during-the-install)
- [3. Building a Raspberry Pi image](raspberry-pi.md)
  - [Building the image](raspberry-pi.md#building-the-image)
  - [Giving each card its settings](raspberry-pi.md#giving-each-card-its-settings)
  - [After the first boot](raspberry-pi.md#after-the-first-boot)
- [4. Roles and machine types](roles.md)
  - [Roles](roles.md#roles)
  - [Machine types](roles.md#machine-types)
  - [Computed defaults](roles.md#computed-defaults)
  - [Cascading implications](roles.md#cascading-implications)
  - [An example host](roles.md#an-example-host)
  - [Overriding values](roles.md#overriding-values)
- [5. Accounts and homes](accounts.md)
  - [Sudo with an SSH key](accounts.md#sudo-with-an-ssh-key)
  - [How a home is assembled](accounts.md#how-a-home-is-assembled)
  - [Personal machines](accounts.md#personal-machines)
  - [Things to know](accounts.md#things-to-know)
- [6. Networking](networking.md)
  - [The firewall](networking.md#the-firewall)
  - [Interface names from the MAC](networking.md#interface-names-from-the-mac)
  - [Wi-Fi networks with networkd](networking.md#wi-fi-networks-with-networkd)
  - [Hotspots](networking.md#hotspots)

### Part III. Running a fleet

- [7. Signing commits and trusting them](signing-and-trust.md)
  - [The signers file](signing-and-trust.md#the-signers-file)
  - [Signing your commits](signing-and-trust.md#signing-your-commits)
  - [Your own copy of the keys](signing-and-trust.md#your-own-copy-of-the-keys)
  - [Your own copy of the trusted commits](signing-and-trust.md#your-own-copy-of-the-trusted-commits)
  - [The tools' own clones](signing-and-trust.md#the-tools-own-clones)
  - [Reviewing new commits](signing-and-trust.md#reviewing-new-commits)
  - [Keys in your agent](signing-and-trust.md#keys-in-your-agent)
  - [Merging](signing-and-trust.md#merging)
  - [Rotating or removing a key](signing-and-trust.md#rotating-or-removing-a-key)
- [8. Secrets](secrets.md)
  - [The secrets repository](secrets.md#the-secrets-repository)
  - [Users with a branch of their own](secrets.md#users-with-a-branch-of-their-own)
  - [Trust model](secrets.md#trust-model)
  - [On the host](secrets.md#on-the-host)
  - [Host secrets](secrets.md#host-secrets)
  - [Rules](secrets.md#rules)
  - [The tools](secrets.md#the-tools)
- [9. A private fleet](private-fleet.md)
  - [Setting one up](private-fleet.md#setting-one-up)
  - [The configuration key](private-fleet.md#the-configuration-key)
  - [Each host gets only its own files](private-fleet.md#each-host-gets-only-its-own-files)
  - [What a host fetches](private-fleet.md#what-a-host-fetches)
  - [Its own CI](private-fleet.md#its-own-ci)
  - [Moving to a newer fleet-manager](private-fleet.md#moving-to-a-newer-fleet-manager)
  - [Installing a host from it](private-fleet.md#installing-a-host-from-it)
  - [Bringing in a running NixOS machine](private-fleet.md#bringing-in-a-running-nixos-machine)
  - [Moving a host fleet-manager defines](private-fleet.md#moving-a-host-fleet-manager-defines)
  - [Moving a running host to it](private-fleet.md#moving-a-running-host-to-it)
- [10. The fleet tool](fleet-tool.md)
  - [Installing it](fleet-tool.md#installing-it)
  - [Without prompts](fleet-tool.md#without-prompts)
  - [A run that stops](fleet-tool.md#a-run-that-stops)
  - [Runbooks](fleet-tool.md#runbooks)
  - [On the installer](fleet-tool.md#on-the-installer)
  - [fleet and fleet-secrets](fleet-tool.md#fleet-and-fleet-secrets)
- [11. Secrets runbooks](secrets-runbooks.md)
  - [Maintainer: one-time setup](secrets-runbooks.md#maintainer-one-time-setup)
  - [Onboarding a machine](secrets-runbooks.md#onboarding-a-machine)
  - [Onboarding a user](secrets-runbooks.md#onboarding-a-user)
  - [Login and sudo keys](secrets-runbooks.md#login-and-sudo-keys)
  - [Host secrets](secrets-runbooks.md#host-secrets)
  - [Day-2 runbooks](secrets-runbooks.md#day-2-runbooks)
  - [Emergency access](secrets-runbooks.md#emergency-access)
  - [Troubleshooting](secrets-runbooks.md#troubleshooting)

### Appendices

- [Appendix A. Configuration reference](configuration.md)
  - [`conf.role`, `conf.machineType`, `conf.platform`](configuration.md#confrole-confmachinetype-confplatform)
  - [`conf.host`](configuration.md#confhost)
  - [`conf.users`](configuration.md#confusers)
  - [`conf.hardware`](configuration.md#confhardware)
  - [`conf.networking` and `conf.sharing`](configuration.md#confnetworking-and-confsharing)
  - [`conf.containers` and `conf.virtualisation`](configuration.md#confcontainers-and-confvirtualisation)
  - [`conf.desktop`](configuration.md#confdesktop)
  - [`conf.development`](configuration.md#confdevelopment)
  - [`conf.flatpak` and `conf.homebrew`](configuration.md#confflatpak-and-confhomebrew)
  - [`conf.fleet`](configuration.md#conffleet)
  - [`conf.secrets`](configuration.md#confsecrets)
- [Appendix B. Things to know](things-to-know.md)
  - [The flake](things-to-know.md#the-flake)
  - [macOS](things-to-know.md#macos)
  - [Hardening and auditing](things-to-know.md#hardening-and-auditing)
  - [Desktops and virtual machines](things-to-know.md#desktops-and-virtual-machines)
  - [Installs](things-to-know.md#installs)
- [Appendix C. Glossary](glossary.md)
- [Appendix D. Further reading](further-reading.md)
  - [How it works](further-reading.md#how-it-works)
  - [Installing a machine](further-reading.md#installing-a-machine)
  - [Building a Raspberry Pi image](further-reading.md#building-a-raspberry-pi-image)
  - [Roles and machine types](further-reading.md#roles-and-machine-types)
  - [Accounts and homes](further-reading.md#accounts-and-homes)
  - [Networking](further-reading.md#networking)
  - [Signing commits and trusting them](further-reading.md#signing-commits-and-trusting-them)
  - [Secrets](further-reading.md#secrets)
  - [A private fleet](further-reading.md#a-private-fleet)
  - [The fleet tool](further-reading.md#the-fleet-tool)
  - [Configuration reference](further-reading.md#configuration-reference)
  - [This book](further-reading.md#this-book)
- [Appendix E. Index](index.md)
<!-- /book:contents -->

<!-- book:bottom -->
---

[1. How it works →](how-it-works.md)
<!-- /book:bottom -->
