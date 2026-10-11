# A throwaway host for testing the secrets design against the local harness
# (tests/secrets/harness.nix). The harness writes harness.nix next to this
# file with the URLs, the support key and the signers file, which depend on
# the machine it runs on. It is not meant to be installed anywhere real.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";

    host.name = "scratch-secrets";

    hardware = {
      boot.mode = "uefi";
      disk = {
        enable = true;
        device = "/dev/vda";
        encrypt = false;
        volumes.swap.size = "1G";
      };
    };

    users.accounts.alice = {
      fullName = "Alice";
      secrets.sshKey = true;
    };
  };
}
