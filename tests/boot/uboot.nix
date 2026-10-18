# Checks conf.hardware.boot.loader = "uboot" on a host built from this flake's
# modules (tests/boot/host), without a board:
# - extlinux.conf is written for the system, the way U-Boot reads it, with
#   GRUB and systemd-boot off;
# - the /boot partition is marked legacy BIOS bootable, where U-Boot looks;
# - "uboot" with mode = "bios", or with efiVariables, is a build error.
# Booting on real U-Boot hardware is not covered.
# Run it with `nix build .#checks.x86_64-linux.boot-uboot`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "ubootbox"; hostDir = ./host; };
  hostWith = extra: inputs.nixpkgs.lib.nixosSystem
    (hostNode // { modules = hostNode.modules ++ [ extra ]; });

  cfg = (hostWith { }).config;
  failedAssertions = extra:
    map (a: a.message) (lib.filter (a: !a.assertion) (hostWith extra).config.assertions);
  rejects = extra: lib.any (lib.hasInfix "loader = \"uboot\"") (failedAssertions extra)
    || lib.any (lib.hasInfix "with loader = \"uboot\"") (failedAssertions extra);

  toplevel = cfg.system.build.toplevel;
in
assert lib.assertMsg cfg.boot.loader.generic-extlinux-compatible.enable "extlinux is not on";
assert lib.assertMsg (!cfg.boot.loader.grub.enable) "GRUB is still on";
assert lib.assertMsg (!cfg.boot.loader.systemd-boot.enable) "systemd-boot is still on";
assert lib.assertMsg (cfg.disko.devices.disk.main.content.partitions.boot.attributes == [ 2 ])
  "/boot is not marked legacy BIOS bootable";
assert lib.assertMsg (rejects { conf.hardware.boot.mode = "bios"; }) "uboot with mode = bios is accepted";
assert lib.assertMsg (rejects { conf.hardware.boot.efiVariables = true; }) "uboot with efiVariables is accepted";
pkgs.runCommand "boot-uboot" { } ''
  ${cfg.boot.loader.generic-extlinux-compatible.populateCmd} -c ${toplevel} -d boot
  conf=boot/extlinux/extlinux.conf
  cat "$conf"
  grep -q "init=${toplevel}/init" "$conf"
  grep -q '^DEFAULT nixos-default' "$conf"
  touch $out
''
