# server1, adopted into the fleet by fleetmgr host adopt, from the
# machine as it ran then. Its own /etc/nixos is kept in previous/, for
# reference only: nothing imports it.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";
  # The release it was installed with, which some services' data depends on.
  system.stateVersion = "24.11";
  boot.loader.grub.device = "/dev/disk/by-id/ata-QEMU_HARDDISK_QM00001";

  conf = {
    host = {
      name = "server1";
      timezone = "Europe/Berlin";
      locale = "en_US.UTF-8";
    };
    role = "server";
    machineType = "headless";
    # Its disks stay as they were installed: hardware-configuration.nix names them.
    hardware.disk.enable = false;
    hardware.boot = {
      mode = "bios";
      loader = "grub";
    };
    networking.backend = "networkd";
    users.accounts.ops = {
      fullName = "Operations";
      privileged = true;
      # It had no password: it stays locked, reached with a key.
      secrets.password = false;
      secrets.authorizedKeys = true;
    };
    # A fleet host: it updates itself from signed commits of this repository,
    # and takes its secrets, support's keys among them, from the secrets
    # repository.
    fleet = {
      repo.url = "git@github.com:example-org/inventory.git";
      signing.allowedSignersFile = ../../../fleet/allowed_signers;
      support.keysFromSecrets = true;
    };
    secrets.repo.url = "git@github.com:example-org/secrets.git";
  };
}
