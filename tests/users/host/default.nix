# The machine in tests/users/ssh-key-sudo.nix, built from this flake's own
# modules: privileged accounts without passwords, one with a sudo key and one
# without, and an unprivileged one. Not under hosts/, so it is never a flake
# output. The keys are filled in by the test, which generates them.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    machineType = "vm";
    host.name = "sudokey";

    # So the login keys are installed too, and can be shown not to unlock sudo.
    sharing.ssh.enable = true;

    users.accounts = {
      admin = {
        fullName = "Admin";
        privileged = true;
      };
      carol = {
        fullName = "Carol";
        privileged = true;
      };
      bob = {
        fullName = "Bob";
        privileged = false;
      };
    };
  };
}
