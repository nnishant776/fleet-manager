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
<!-- /book:contents -->
