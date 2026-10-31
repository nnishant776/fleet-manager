# NixOS / nix-darwin Configuration Flake

A multi-platform configuration flake for NixOS, macOS and Home Manager
environments. Machines are declared under `hosts/`, per-user configuration under
`users/`, and behaviour is driven by the `conf.*` option tree. A machine's
[role](docs/roles.md) sets defaults that any host can override. The `conf.*`
option tree is a generalised group of simple toggles for most common config
recipes that can be applied on hosts without hardcoding. It is also toggleable
which helps move more of the logic to a common configuration module rather than
being per host.

It also runs a fleet. Each machine pulls its configuration from git on a timer,
builds only commits signed by a trusted key, and decrypts its own secrets from
a separate private repository. A real fleet keeps its hosts in a private
inventory that pins this repository. [How it works](docs/how-it-works.md)
explains the pieces, and the [guide](docs/README.md) has the rest.

## Repository Layout

```
├── flake.nix                        # entry point; discovers hosts/
├── docs/                            # the guide, a book: how it works, installing, configuration, secrets
├── bootstrap                        # first-run script; see First run below
├── fleet/
│   └── allowed_signers              # keys the fleet accepts on commits; see docs/signing-and-trust.md
├── lib/
│   ├── apps/                        # os-install, system-switch, home-switch, check-purity, fleet-secrets, fleet; rpi-image and secrets-harness on Linux; darwin-rebuild on macOS
│   ├── mkFleet.nix                  # lib.mkFleet: a fleet's systems, homes and apps from its host folders; see docs/private-fleet.md
│   ├── mkHost.nix                   # host factory (NixOS / nix-darwin)
│   ├── secrets-sync.nix             # fleet-secrets-sync and fleet-identity, used by the sync and os-install
│   ├── clone-flake.nix              # fleet-clone-flake: clones a private flake for an install, at an exact commit
│   ├── forge-host-keys.nix          # GitHub's SSH host keys, which every fetch over SSH is checked against
│   ├── buildUser.nix                # assembles a user's Home Manager modules
│   ├── mkUser.nix                   # standalone Home Manager builder
│   └── flakeLib.nix                 # helpers shared by this flake's modules
├── hosts/<hostname>/                # one directory per machine, auto-discovered
│   ├── default.nix                  # nixpkgs.hostPlatform and conf.* settings
│   ├── hardware-configuration.nix   # Linux only, from nixos-generate-config
│   └── *.nix                        # every file here is imported as a module
├── users/<username>/                # optional per-user configuration
│   └── default.nix                  # merged into that user's home, both paths
├── tests/                           # VM and evaluation checks wired into the flake's `checks`
│   │                                # one folder per area: boot, fleet, lib (shared helpers), networking, pi-gw, rpi-image, secrets, users
│   └── secrets/                     # also holds secrets-harness, the local stand-in for the secrets repository
├── tools/fleet/                     # fleet, the guided command-line tool; runbooks/ has a page per command
└── modules/
    ├── conf/                        # the conf.* vocabulary: options/<concern>.nix, role.nix, machineType.nix
    ├── common/                      # configuration valid on both platforms
    ├── linux/                       # NixOS implementation, one folder per concern
    ├── darwin/                      # nix-darwin implementation
    └── user/                        # Home Manager baseline for managed users
```

Most concerns have one option file in `modules/conf/options/` and one folder
or file of the same name under `modules/linux/`, so the word in `conf.<name>`
usually finds both the declaration and the implementation. The exceptions are
`conf.hardware`, implemented in `baseline/`, `graphics/` and `power/`, and
`conf.users`, implemented in `host/` and `secrets/`. A folder's `default.nix` only
imports its parts; each part sets its own options under its own `mkIf`.

## Usage

### First run

On a machine this flake does not configure yet, one command does everything:

```bash
curl -sSfL https://raw.githubusercontent.com/nnishant776/fleet-manager/main/bootstrap | sh -s -- <hostname>
```

`bootstrap` installs Nix where it is missing — the one step Nix cannot do for
itself — and then hands over to the flake's own tools:

| Running on | It does |
|---|---|
| macOS | installs Lix if needed, then switches to `<hostname>` with the nix-darwin this flake pins |
| NixOS installer ISO | runs `fleet install`, a guided `os-install`: built from `REV` when given, otherwise downloaded from the latest release and checked against its `SHA256SUMS`; if neither works, `os-install` by itself. `<hostname>` is optional, and further arguments are passed on |
| an installed NixOS host | runs `system-switch` for `<hostname>` (default: this machine's hostname), building from the same flake |
| another Linux | installs Lix and stops; only Home Manager is supported there |

`FLAKE=github:org/repo/<tag>` in front of `sh` makes it use another flake, or a
pinned tag so that machines do not follow every commit on `main`. MDM can run
the same command on a new Mac.

To install only commits whose signatures you have checked, to set up a Mac,
or to install without `bootstrap`, see
[Installing a machine](docs/installing.md).

### Adding a machine

Create `hosts/<hostname>/default.nix` with `nixpkgs.hostPlatform` and the
machine's `conf.*` settings. No change to `flake.nix` is needed. The directory
name must be exactly the machine's hostname. The host must also set
`conf.host.name` to that same name; evaluation fails when the two differ.

Anything the `conf` vocabulary does not cover is written as a plain NixOS or
nix-darwin option in the same file, after the `conf` block. Every `.nix` file
in the host directory is imported, so `hardware-configuration.nix`,
`disko-config.nix` or a `network.nix` holding `networking.*` and
`systemd.network.*` settings need no import line.

An example host:

```nix
# hosts/my-laptop/default.nix
{ pkgs, ... }: {
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "developer";
    machineType = "laptop";

    host.name = "my-laptop";

    users.accounts.alice = {
      fullName = "Alice";
      privileged = true;
      selfManagedHome = true;
    };

    containers.enable = false;              # the role enables it
    hardware.graphics.vendor = "nvidia";

    desktop = {
      environments.hyprland = {
        enable = true;
        shell = "caelestia";
      };
      extraPackages = with pkgs; [ discord spotify ];
    };

    development.sdk.go.enable = true;
  };

  # Anything conf does not cover is a plain option, here.
  services.cloudflare-warp.enable = true;
}
```

Every option is in the [configuration reference](docs/configuration.md).

### Building and switching a system

```bash
nixos-rebuild  switch --flake .#<hostname>     # Linux
darwin-rebuild switch --flake .#<hostname>     # macOS
system-switch [host] [flake]                   # wrapper; defaults to this host and /etc/nixos; flake is a directory or a reference
```

On a machine whose configuration lives in `/etc/nixos`, `nixos-rebuild switch`
with no arguments works, because that is where it looks when given no flake.

### Switching a home configuration

Only users with `selfManagedHome` have a home configuration to activate. For
everyone else a system rebuild activates the home.

```bash
home-switch                                    # this user, /etc/nixos, this host
home-switch <username> <flake path> <host>     # explicit
home-manager switch --flake /etc/nixos --impure   # the bare CLI resolves <user>@<host> itself
```

`/etc/nixos` belongs to root, and git refuses to work in a repository that
another user owns. On NixOS the system tells git to trust this one checkout for
every account (`safe.directory` in `/etc/gitconfig`). So these commands work
without anything in your own git configuration, even before your home
configuration exists. Other root-owned repositories are not trusted.

### The `fleet` tool

`fleet` walks through the fleet's procedures one step at a time: installing a
machine, enrolling a host, secrets, the support key, and checks. Each step says
what it does and why, shows the exact command, asks before it changes
anything, and checks the result. `fleet` alone shows a menu, and
`fleet <command> --help` explains a command.

It is not installed anywhere by default.
Install it yourself where you want it:

```bash
nix profile install /etc/nixos#fleet        # on a NixOS machine of the fleet
nix profile install .#fleet                 # or from a checkout you have checked
nix run .#fleet -- host check pi1   # once, without installing
nix profile upgrade fleet                   # later, for a newer version
```

Install it from a checkout you have checked, not with `github:` straight from
main; [The fleet tool](docs/fleet-tool.md) says why, and how it runs. Each
command has a page in the [runbooks](tools/fleet/runbooks/README.md).

### Checking the flake

```bash
nix run .#check-purity                         # every system output must evaluate without --impure
nix eval .#nixosConfigurations.<host>.config.conf.hardware.graphics.vendor
```

## Documentation

The [guide](docs/README.md) is a book in three parts: how a fleet fits
together, machines, and running a fleet, with the configuration reference in
an appendix. Start with [How it works](docs/how-it-works.md), or look things
up in its [contents](docs/README.md#contents), [glossary](docs/glossary.md)
and [index](docs/index.md).
