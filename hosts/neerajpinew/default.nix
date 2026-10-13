# A Raspberry Pi 5: a plain headless machine on the card written by
# `nix run .#rpi-image`. Copy this directory to start a new Pi host.
#
# To bring a card up as this host:
#   1. Flash the image and put `neerajpinew` in firstboot/hostname on its FAT
#      partition, plus authorized_keys (and wifi, if it has no cable).
#   2. Boot it, SSH in as `nixos`, and switch:
#        sudo nixos-rebuild switch --flake <this flake>#neerajpinew
#   The first boot adds the card's Wi-Fi network to
#   /etc/wpa_supplicant/imperative.conf, which this host reads too (networkd
#   backend), so the Pi stays on the same Wi-Fi after the switch.
#
# This file only takes { config, pkgs, lib } so flake.nix can read the platform
# from it; the board support, which needs `inputs`, is in hardware.nix.
{ config, pkgs, lib, ... }: {
  nixpkgs.hostPlatform = "aarch64-linux";

  conf = {
    role = "minimal";
    machineType = "headless";

    # The same network setup as the card: systemd-networkd and wpa_supplicant.
    networking = {
      backend = "networkd";
      wireless = {
        countryCode = "IN";
        # This Pi's network is Wi-Fi; headless machines leave it off by default.
        wifi.enable = true;
      };
    };

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
