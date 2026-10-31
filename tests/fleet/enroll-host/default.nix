# The machine in tests/fleet/enroll.nix: a running host whose configuration
# takes secrets, but which is not enrolled yet. The keys and repository URLs
# come from the test. Not under hosts/, so it is never a flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "newhost";

    users.accounts.admin = {
      fullName = "Admin";
      privileged = true;
      # Key only, as on a Pi host.
      secrets.password = false;
    };
  };
}
