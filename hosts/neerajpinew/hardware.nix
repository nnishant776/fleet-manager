# Raspberry Pi 5 board support from nixos-raspberrypi: vendor kernel,
# firmware and its generational bootloader, as in hosts/pi-gw/hardware.nix.
# This is what its own lib.nixosSystem would add, assembled here because mkHost
# builds every host. Its modules read a `nixos-raspberrypi` module argument,
# which mkHost does not pass; they use it only in option values, so
# _module.args is enough.
{ inputs, lib, ... }: {
  imports = with inputs.nixos-raspberrypi.nixosModules; [
    # Vendor kernel, firmware and package overlays, and pkgs.rpi.
    inputs.nixos-raspberrypi.lib.int.default-nixos-raspberrypi-config
    raspberry-pi-5.base
    # Prebuilt vendor kernel instead of compiling it on the Pi.
    trusted-nix-caches
  ];
  _module.args.nixos-raspberrypi = inputs.nixos-raspberrypi;

  # The Pi boots through its firmware, not EFI, with the loader the
  # nixos-raspberrypi modules above bring, so this flake turns on none.
  conf.hardware.boot.loader = null;
  # The loader the rpi-image installer boots with, so switching to this host
  # keeps it; assembled this way the board module would pick the deprecated
  # "kernelboot" instead.
  boot.loader.raspberry-pi.bootloader = "kernel";

  # The partitions of the card written by `nix run .#rpi-image`, so the host
  # runs from the card it was installed from; no repartitioning is needed.
  fileSystems."/" = {
    device = "/dev/disk/by-label/NIXOS_SD";
    fsType = "ext4";
    options = [ "noatime" ];
  };
  fileSystems."/boot/firmware" = {
    device = "/dev/disk/by-label/FIRMWARE";
    fsType = "vfat";
    options = [ "noatime" "noauto" "x-systemd.automount" "x-systemd.idle-timeout=1min" ];
  };
}
