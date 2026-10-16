# Test of per-account sudo by SSH key (conf.users.accounts.<name>.sudoSshKeys)
# on a host built from this flake's modules (tests/users/host). The checks:
# - a privileged account gets sudo by a key in its agent that is one of its
#   sudoSshKeys, with no password;
# - its login key does not unlock sudo, nor does having no agent;
# - a privileged account without sudoSshKeys gets no sudo by its key;
# - an unprivileged account gets no sudo, whatever key it holds.
# Run it with `nix build .#checks.x86_64-linux.users-ssh-key-sudo`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "sudokey"; hostDir = ./host; };

  keys = pkgs.runCommand "ssh-key-sudo-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    for key in admin-login admin-sudo carol; do
      ssh-keygen -q -t ed25519 -N "" -C "$key" -f "$out/$key"
    done
  '';
  pub = key: lib.removeSuffix "\n" (builtins.readFile "${keys}/${key}.pub");

  # Starts an agent holding one key, then runs a command as the given user
  # with that agent, as `ssh -A` would provide it.
  withAgent = pkgs.writeShellScript "with-agent" ''
    user=$1 key=$2; shift 2
    install -m 0600 -o "$user" "${keys}/$key" "/tmp/$user-$key"
    exec runuser -u "$user" -- sh -c '
      eval "$(ssh-agent -s)" >/dev/null
      ssh-add -q "$0"
      "$@"
      status=$?
      ssh-agent -k >/dev/null
      exit $status
    ' "/tmp/$user-$key" "$@"
  '';
in
pkgs.testers.runNixOSTest {
  name = "users-ssh-key-sudo";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.sudokey = {
    imports = hostNode.modules ++ [{
      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      conf.users.accounts = {
        admin = {
          sshKeys = [ (pub "admin-login") ];
          sudoSshKeys = [ (pub "admin-sudo") ];
        };
        carol.sshKeys = [ (pub "carol") ];
      };
      environment.systemPackages = [ pkgs.openssh ];
    }];
  };

  testScript = ''
    sudokey.wait_for_unit("multi-user.target")

    # sudo remembers a success for a while on the same terminal; forget it
    # before each check that must be refused.
    def refused(user, command):
        sudokey.succeed(f"runuser -u {user} -- sudo -K")
        sudokey.fail(command)

    with subtest("admin's sudo key in its agent gives sudo, with no password"):
        sudokey.succeed("${withAgent} admin admin-sudo sudo -n true")
        sudokey.succeed('test "$(${withAgent} admin admin-sudo sudo -n id -u)" = 0')

    with subtest("admin's login key, or no agent, does not"):
        refused("admin", "${withAgent} admin admin-login sudo -n true")
        refused("admin", "runuser -u admin -- sudo -n true")

    with subtest("a privileged account without sudo keys gets no sudo by its key"):
        refused("carol", "${withAgent} carol carol sudo -n true")

    with subtest("an unprivileged account gets no sudo, whatever key it holds"):
        sudokey.fail("${withAgent} bob admin-sudo sudo -n true")

    with subtest("only accounts with sudo keys have a sudo key file"):
        sudokey.succeed("test -f /etc/ssh/sudo_keys.d/admin")
        sudokey.fail("test -e /etc/ssh/sudo_keys.d/carol")
        sudokey.succeed('test "$(stat -c %U /etc/ssh/sudo_keys.d/admin)" = root')
  '';
}
