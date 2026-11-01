# desk1, set up by fleetmgr host new before it was installed.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";
  # The release it is installed with, which some services' data depends on;
  # it stays when the fleet moves to a newer one.
  system.stateVersion = "26.05";

  conf = {
    host = {
      name = "desk1";
      timezone = "Europe/Berlin";
      locale = "en_US.UTF-8";
    };
    role = "developer";
    machineType = "desktop";
    # The installer lays the disk out from this: os-install -d can name
    # another disk at install time.
    hardware.disk = {
      enable = true;
      device = "/dev/nvme0n1";
      encrypt = true;
    };
    hardware.boot = {
      mode = "uefi";
      loader = "systemd-boot";
    };
    networking.backend = "networkmanager";
    users.accounts.alice = {
      fullName = "Alice";
      privileged = true;
      # The hash of the password set for it, from the secrets.
      secrets.password = true;
      secrets.authorizedKeys = true;
    };
    users.accounts.bob = {
      fullName = "Bob \"the builder\" \${x}";
      privileged = false;
      groups = [ "audio" "video" ];
      # No password: it stays locked, reached with a key.
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
