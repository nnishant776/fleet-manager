# Further reading

For each chapter, this appendix lists the upstream documentation it builds
on and where its subject lives in the code. The code is the final word: when
this book and the code disagree, the code is right, and the book has a bug.

## How it works

- [Nix reference manual](https://nix.dev/manual/nix/latest/), and its
  [flakes](https://nix.dev/manual/nix/latest/command-ref/new-cli/nix3-flake)
  page.
- [NixOS manual](https://nixos.org/manual/nixos/stable/): modules, options,
  and how definitions merge (`mkDefault`, `mkForce`).
- [systemd.timer](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html)
  and [systemd.time](https://www.freedesktop.org/software/systemd/man/latest/systemd.time.html),
  for the sync's schedule.
- In the code: [`lib/mkFleet.nix`](../lib/mkFleet.nix) and
  [`lib/mkHost.nix`](../lib/mkHost.nix) build the hosts;
  [`modules/conf/`](../modules/conf/) is the vocabulary;
  [`modules/linux/fleet/sync.nix`](../modules/linux/fleet/sync.nix) is the
  sync, [`bootstrap.nix`](../modules/linux/fleet/bootstrap.nix) the first-boot
  clone, and [`sync-status.nix`](../modules/linux/fleet/sync-status.nix) the
  notice everyone sees.

## Installing a machine

- [disko](https://github.com/nix-community/disko), which makes the disk layout.
- [lvm(8)](https://man7.org/linux/man-pages/man8/lvm.8.html),
  [cryptsetup](https://gitlab.com/cryptsetup/cryptsetup) for LUKS, and
  [systemd-cryptenroll](https://www.freedesktop.org/software/systemd/man/latest/systemd-cryptenroll.html)
  for the TPM.
- [nix-darwin manual](https://nix-darwin.github.io/nix-darwin/manual/) and
  [Lix](https://lix.systems/), for a Mac.
- In the code: [`bootstrap`](../bootstrap);
  [`lib/apps/os-install.nix`](../lib/apps/os-install.nix);
  [`modules/linux/baseline/disk.nix`](../modules/linux/baseline/disk.nix), the
  layout and `disk-extend`; [`lib/clone-flake.nix`](../lib/clone-flake.nix),
  the clone of a private flake.

## Building a Raspberry Pi image

- [nixos-raspberrypi](https://github.com/nvmd/nixos-raspberrypi), the board
  support and the installer image.
- In the code: [`lib/apps/rpi-image/`](../lib/apps/rpi-image/).

## Roles and machine types

- The [NixOS manual](https://nixos.org/manual/nixos/stable/) on option
  priorities, which decide what overrides a role's defaults.
- In the code: [`modules/conf/role.nix`](../modules/conf/role.nix) and
  [`modules/conf/machineType.nix`](../modules/conf/machineType.nix).

## Accounts and homes

- [Home Manager manual](https://nix-community.github.io/home-manager/).
- [pam_ssh_agent_auth](https://github.com/jbeverly/pam_ssh_agent_auth), behind
  sudo with an SSH key, and [ssh-add(1)](https://man.openbsd.org/ssh-add) for
  `-c` and `-t`.
- In the code: [`modules/conf/options/users.nix`](../modules/conf/options/users.nix);
  [`modules/linux/host/`](../modules/linux/host/), accounts and privilege;
  [`lib/buildUser.nix`](../lib/buildUser.nix) and
  [`modules/user/`](../modules/user/), homes;
  [`modules/linux/fleet/personal.nix`](../modules/linux/fleet/personal.nix),
  personal machines; [`modules/linux/fleet/home-drift.nix`](../modules/linux/fleet/home-drift.nix),
  the notice about a home that fell behind.

## Networking

- [nftables wiki](https://wiki.nftables.org/).
- [NetworkManager](https://networkmanager.dev/),
  [systemd.network](https://www.freedesktop.org/software/systemd/man/latest/systemd.network.html)
  and [systemd-resolved](https://www.freedesktop.org/software/systemd/man/latest/systemd-resolved.service.html).
- [wpa_supplicant](https://w1.fi/wpa_supplicant/) and
  [hostapd](https://w1.fi/hostapd/).
- In the code: [`modules/linux/networking/`](../modules/linux/networking/):
  `firewall.nix`, `names.nix`, `networkd.nix`, `hotspot.nix`.

## Signing commits and trusting them

- [git-config](https://git-scm.com/docs/git-config) (`gpg.format`,
  `gpg.ssh.allowedSignersFile`) and
  [git-verify-commit](https://git-scm.com/docs/git-verify-commit).
- [ssh-keygen(1)](https://man.openbsd.org/ssh-keygen), its ALLOWED SIGNERS
  section for the file's format.
- [GitHub's SSH key fingerprints](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints),
  which every fetch over SSH is checked against.
- In the code: [`fleet/allowed_signers`](../fleet/allowed_signers);
  [`lib/forge-host-keys.nix`](../lib/forge-host-keys.nix);
  [`tools/fleet/flows/trust.go`](../tools/fleet/flows/trust.go),
  [`repo.go`](../tools/fleet/flows/repo.go) and
  [`repo_sync.go`](../tools/fleet/flows/repo_sync.go), the tools' copies and
  clones.

## Secrets

- [sops](https://github.com/getsops/sops),
  [sops-nix](https://github.com/Mic92/sops-nix) and
  [age](https://github.com/FiloSottile/age).
- [Managing deploy keys](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys)
  on GitHub.
- In the code: [`lib/secrets-sync.nix`](../lib/secrets-sync.nix),
  `fleet-secrets-sync` and `fleet-identity`;
  [`modules/linux/secrets/`](../modules/linux/secrets/), sops and the users'
  secrets; [`lib/apps/fleet-secrets.nix`](../lib/apps/fleet-secrets.nix), the
  shell tool; [`tests/secrets/`](../tests/secrets/), with the local stand-in
  for the secrets repository.

## A private fleet

- In the code: [`lib/mkFleet.nix`](../lib/mkFleet.nix), host branches
  included; [`tools/fleet/flows/inventory_publish.go`](../tools/fleet/flows/inventory_publish.go),
  [`inventory_pin.go`](../tools/fleet/flows/inventory_pin.go),
  [`inventory_import.go`](../tools/fleet/flows/inventory_import.go) and
  [`host_enroll.go`](../tools/fleet/flows/host_enroll.go).

## The fleet tool

- [Using artifact attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds),
  for `gh attestation verify` on a release.
- In the code: [`tools/fleet/`](../tools/fleet/); each command is a flow in
  [`tools/fleet/flows/`](../tools/fleet/flows/), and its runbook in
  [`tools/fleet/runbooks/`](../tools/fleet/runbooks/README.md) is written from
  it.

## Configuration reference

- [NixOS option search](https://search.nixos.org/options), for the plain
  options a host sets beside `conf.*`.
- [ReGreet](https://github.com/rharish101/ReGreet), the greeter, and
  [Plymouth](https://gitlab.freedesktop.org/plymouth/plymouth), the boot
  splash.
- [Chromium's enterprise policies](https://chromeenterprise.google/policies/),
  for `desktop.browserPolicies`.
- [containers-registries.conf](https://github.com/containers/image/blob/main/docs/containers-registries.conf.5.md),
  for `containers.registries`; [libvirt](https://libvirt.org/);
  [Flatpak](https://docs.flatpak.org/).
- [Grafana Alloy](https://grafana.com/docs/alloy/latest/) and
  [Loki](https://grafana.com/docs/loki/latest/), for `fleet.logs.lokiUrl`, and
  [auditd(8)](https://man7.org/linux/man-pages/man8/auditd.8.html).
- In the code: [`modules/conf/options/`](../modules/conf/options/), one file
  per concern, and its implementation under
  [`modules/linux/`](../modules/linux/) and
  [`modules/darwin/`](../modules/darwin/).

## This book

- [mdBook's SUMMARY.md format](https://rust-lang.github.io/mdBook/format/summary.html),
  which [`SUMMARY.md`](SUMMARY.md) follows.
- `fleet docs` writes the contents, the page turns and the index from it; see
  its [runbook](../tools/fleet/runbooks/docs.md).
