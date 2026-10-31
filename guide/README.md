# Guide

The [README](../README.md) has the basics: first run, adding a machine,
building and switching, and the `fleet` tool. This guide has the rest.

Start with [How it works](how-it-works.md) for how the pieces fit.

| Page | What it covers |
|---|---|
| [How it works](how-it-works.md) | The repositories, the maintainer's machine and the hosts; how `conf.*` becomes a system; what a host does at each sync |
| [Installing a machine](installing.md) | Checked installs, macOS, `os-install`, the disk layout, adding a disk, enrollment during the install |
| [Building a Raspberry Pi image](raspberry-pi.md) | One SD image for every Pi 5, with each card's own settings |
| [Configuration reference](configuration.md) | Every `conf.*` option, by concern |
| [Roles and machine types](roles.md) | The defaults each sets, computed defaults, what each option turns on, and how to override |
| [Accounts and homes](accounts.md) | sudo with an SSH key, how a home is assembled, personal machines, what to know about accounts |
| [Networking](networking.md) | The firewall, interface names from the MAC, Wi-Fi networks with networkd, hotspots |
| [Signing commits and trusting them](signing-and-trust.md) | The signers file, your own copies of keys and trusted commits, the tools' own clones, rotating keys |
| [A private fleet](private-fleet.md) | An inventory of your own, host branches, publishing, pinning, installing and bringing machines in |
| [Secrets](secrets.md) | The secrets repository, the trust model, what a host does with its secrets, host secrets, the rules |
| [Secrets runbooks](secrets-runbooks.md) | Setup, onboarding machines and users, keys, day-2 work, emergency access, troubleshooting |
| [The fleet tool](fleet-tool.md) | Installing it, running it without prompts, what a stopped run leaves, releases |
| [Things to know](things-to-know.md) | Behaviour that surprises people |

Each `fleet` command also has a page of its own in the
[runbooks](../tools/fleet/runbooks/README.md), written from the command itself.
