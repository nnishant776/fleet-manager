# laptop3 as the fleet runs it once adopted: what fleetmgr host adopt writes
# from a skeleton, less what the test VM does differently (it boots without
# a loader). The test builds it as a specialisation of the machine.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";
  conf = {
    host = {
      name = "laptop3";
      timezone = "Europe/Berlin";
      locale = "en_US.UTF-8";
    };
    role = "server";
    machineType = "vm";
    hardware.disk.enable = false;
    hardware.boot.loader = null;
    users.accounts.alice = {
      fullName = "Alice";
      privileged = true;
      groups = [ "audio" ];
      secrets.password = true;
      secrets.authorizedKeys = true;
    };
    users.accounts.bob = {
      fullName = "Bob";
      secrets.password = false;
    };
  };
}
