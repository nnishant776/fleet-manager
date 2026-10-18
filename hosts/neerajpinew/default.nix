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
      # Interfaces are named after their bus and MAC from the next boot: the
      # built-in Wi-Fi becomes wls<MAC>, the Ethernet enp<MAC>. `ip link`
      # shows them.
      macNames = true;
      wireless = {
        countryCode = "IN";
        # This Pi's network is Wi-Fi; headless machines leave it off by default.
        # With no interfaces named, it uses every Wi-Fi interface, from
        # /etc/wpa_supplicant/imperative.conf.
        wifi.enable = true;
        # To add a hotspot on a USB adapter: plug it in and read its name
        # with `ip link` (wlu<MAC>). Name the client's interface, so it never
        # takes the hotspot's adapter, and put the hotspot's password in
        # /var/lib/hotspot/<name> (root, mode 0600):
        #
        #   wifi.interfaces.wls<MAC of the built-in Wi-Fi> = { };
        #   hotspots.wlu<MAC of the adapter> = { enable = true; };
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
      # No password, from the secrets repository or anywhere else: admin logs
      # in and gets sudo by SSH key only.
      initialHashedPassword = null;
      secrets.password = false;
      # Password login over SSH is off. The image's `nixos` user and its keys
      # go away on the switch, so put a key here first or the Pi is reachable
      # from its console only.
      sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAVFepUnXSeStB3vOnVM4poqOy1ETJEgyH0UAbP5dF0e Home Network SSH Key"
      ];
      # admin has no password, so sudo takes this key instead, from the agent
      # brought along with `ssh -A`. The login key for now; a separate key can
      # replace it here alone. Not a hardware key: the sudo check
      # (pam_ssh_agent_auth) does not support them.
      sudoSshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAVFepUnXSeStB3vOnVM4poqOy1ETJEgyH0UAbP5dF0e Home Network SSH Key"
      ];
    };

    sharing = {
      enable = true;
      ssh.enable = true;
    };

    # A fleet host: it updates itself from signed commits on main, and takes its
    # secrets from the private repository, fetched with its own deploy key.
    fleet = {
      repo.url = "https://github.com/nnishant776/fleet-manager.git";
      signing.allowedSignersFile = ../../fleet/allowed_signers;
      # The maintainer's way in: key only, with sudo and no password. The same
      # key as admin's for now, until a separate one replaces it.
      support.sshKeys = [
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAVFepUnXSeStB3vOnVM4poqOy1ETJEgyH0UAbP5dF0e Home Network SSH Key"
      ];
    };
    secrets.repo.url = "git@github.com:nnishant776/fleet-secrets.git";
  };

  # Root fetches the secrets over SSH and has no known_hosts of its own, so
  # GitHub's host key is pinned here (checked against `gh api meta`).
  programs.ssh.knownHosts."github.com".publicKey =
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl";
}
