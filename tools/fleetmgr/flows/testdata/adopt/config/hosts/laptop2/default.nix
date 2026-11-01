# laptop2, adopted into the fleet by fleetmgr host adopt, from the
# machine as it ran then. Its own configuration is in previous/, as
# it was; this file adds only what the fleet needs.
{ lib, ... }:
{
  imports = [ ./previous/configuration.nix ];

  # Read by the fleet before anything is evaluated, so it is set here too.
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    # The same as its own configuration's, which the fleet sets as well.
    host = {
      name = "laptop2";
      timezone = "Europe/Berlin";
      locale = "en_US.UTF-8";
    };
    role = "workstation";
    machineType = "laptop";
    # Its disks and its boot loader stay as its own configuration has them.
    hardware.disk.enable = false;
    hardware.boot.loader = null;
    networking.backend = "networkmanager";
    users.accounts.alice = {
      fullName = "Alice";
      privileged = true;
      # The hash it had, from the secrets.
      secrets.password = true;
      secrets.authorizedKeys = true;
    };
    users.accounts.bob = {
      fullName = "Bob \"the builder\" \${x}";
      privileged = false;
      groups = [ "audio" "video" ];
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

  # A host with secrets takes login keys and passwords from them only; the
  # old configuration's are put aside.
  users.users = {
    root = {
      openssh.authorizedKeys.keys = lib.mkForce [ ];
      openssh.authorizedKeys.keyFiles = lib.mkForce [ ];
    };
    alice = {
      openssh.authorizedKeys.keys = lib.mkForce [ ];
      openssh.authorizedKeys.keyFiles = lib.mkForce [ ];
      hashedPassword = lib.mkForce null;
      initialPassword = lib.mkForce null;
      initialHashedPassword = lib.mkForce null;
      password = lib.mkForce null;
    };
    bob = {
      openssh.authorizedKeys.keys = lib.mkForce [ ];
      openssh.authorizedKeys.keyFiles = lib.mkForce [ ];
    };
  };
}
