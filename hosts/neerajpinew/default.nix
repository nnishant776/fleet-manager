# Sample Raspberry Pi 5: a plain headless machine on the card written by
# `nix run .#rpi-image`. Copy this directory to start a new Pi host; for a Pi
# that does more (routing, microVMs), see hosts/pi-gw.
#
# To bring a card up as this host:
#   1. Flash the image and put `rpi5` in firstboot/hostname on its FAT
#      partition, plus authorized_keys (and wifi, if it has no cable).
#   2. Boot it, SSH in as `nixos`, and switch:
#        sudo nixos-rebuild switch --flake <this flake>#rpi5
#   The Wi-Fi network added on first boot is a NetworkManager connection in
#   /etc, so it survives the switch.
#
# This file only takes { config, pkgs, lib } so flake.nix can read the platform
# from it; the board support, which needs `inputs`, is in hardware.nix.
{ config, pkgs, lib, ... }: {
  nixpkgs.hostPlatform = "aarch64-linux";

  conf = {
    # minimal brings NetworkManager, which the image's Wi-Fi connection uses.
    # Do not set networking.wifi.enable: it selects the standalone
    # wpa_supplicant backend, which conflicts with NetworkManager.
    role = "minimal";
    machineType = "headless";

    host = {
      name = "neerajpinew";
      timezone = "Asia/Kolkata";
      locale = "en_IN";
    };

    users.accounts.admin = {
      fullName = "Administrator";
      email = "admin@example.com";
      privileged = true;
      # mkpasswd format; a placeholder here, as in hosts/example.
      initialHashedPassword = null;
      # Password login over SSH is off. The image's `nixos` user and its keys
      # go away on the switch, so put a key here first or the Pi is reachable
      # from its console only.
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAVFepUnXSeStB3vOnVM4poqOy1ETJEgyH0UAbP5dF0e Home Network SSH Key"
      ];
    };

    sharing = {
      enable = true;
      ssh.enable = true;
    };
  };
}
