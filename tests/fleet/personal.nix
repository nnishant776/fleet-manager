# Test personal mode (conf.fleet.personal) on two hosts built from this flake's
# modules (tests/fleet/personal-host) whose upstream is a local repository:
# `owner`, whose owner is an administrator, and `plain`, whose is not. Each
# also declares an ordinary fleet account, bob. The checks:
# - the first boot shows the account setup on the console, not a login prompt;
#   it takes a username, a full name and a password twice, makes the account
#   and says so, and the login prompt follows;
# - the account has its name, home and groups (wheel on `owner` only), and
#   /var/lib/fleet/owner names it;
# - a name that is bad (not lowercase letters and digits), reserved (root) or
#   declared (bob) is refused and asked for again;
# - the owner logs in at the console with the password given;
# - activating the configuration again keeps the account, its password hash
#   and its groups, and the setup does not run again, nor after a reboot;
# - the owner's own ~/.ssh/authorized_keys admits her over SSH, while the
#   same key in bob's does not;
# - on `plain`, the owner has no sudo and only the allowlisted NetworkManager
#   actions; on `owner` she has sudo with her password.
# Wi-Fi is not covered: the VM has none, and has a network, so the setup never
# offers nmtui. Run it with `nix build .#checks.x86_64-linux.fleet-personal`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "checkout"; hostDir = ./personal-host; };

  keys = pkgs.runCommand "fleet-personal-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    ssh-keygen -q -t ed25519 -N "" -C signer -f $out/signer
    echo "signer namespaces=\"git\" $(cut -d' ' -f1,2 $out/signer.pub)" > $out/allowed_signers
    ssh-keygen -q -t ed25519 -N "" -C owner -f $out/owner
  '';

  node = ownerPrivileged: {
    imports = hostNode.modules ++ [{
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;
      # The test framework turns switching off unless a test asks for it.
      system.switch.enable = lib.mkForce true;

      conf.fleet = {
        repo.url = "file:///srv/upstream.git";
        signing.allowedSignersFile = "${keys}/allowed_signers";
        personal = {
          enable = true;
          inherit ownerPrivileged;
        };
      };
      # What modules/linux/sharing/ssh.nix sets on a host with secrets, which
      # this one is not: no account reads keys from its home, so only the
      # fleet-local match lets the owner do it.
      services.openssh.authorizedKeysInHomedir = false;
      systemd.services.fleet-bootstrap.enable = false;
      systemd.timers.config-auto-update.enable = false;
    }];
  };
in
pkgs.testers.runNixOSTest {
  name = "fleet-personal";

  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.owner = node true;
  nodes.plain = node false;

  testScript = ''
    start_all()
    password = "correct horse"

    def setup(m, admin):
        # The setup holds the console; no login prompt yet.
        m.wait_until_tty_matches("1", "Username: ")
        assert "login: " not in m.get_tty_text("1")
        m.wait_until_tty_matches("1", "administrator" if admin else "ordinary")

        with_name = [("Bad Name", "starts with a lowercase"), ("root", "root is reserved"), ("bob", "bob is reserved")]
        for bad, message in with_name:
            m.send_chars(bad + "\n")
            m.wait_until_tty_matches("1", message)
        m.send_chars("ann\n")
        m.wait_until_tty_matches("1", "Full name")
        m.send_chars("Ann Example\n")
        m.wait_until_tty_matches("1", "Password: ")
        m.send_chars(password + "\n")
        m.wait_until_tty_matches("1", "Password again: ")
        m.send_chars(password + "x\n")
        m.wait_until_tty_matches("1", "The two passwords differ")
        m.send_chars(password + "\n")
        m.wait_until_tty_matches("1", "Password again: ")
        m.send_chars(password + "\n")
        m.wait_until_tty_matches("1", "Your account ann is ready")
        m.wait_until_tty_matches("1", "login: ")

    with subtest("an activation while the setup waits does not wait for the owner"):
        # As the first sync would do on a machine nobody has set up yet: the
        # setup holds the boot until the owner is done, and activation must
        # not hang on that.
        owner.wait_until_tty_matches("1", "Username: ")
        status, out = owner.execute("timeout 300 /run/current-system/bin/switch-to-configuration test 2>&1")
        assert status == 0, f"activation while the setup waits exited {status}:\n{out}"
        owner.succeed("systemctl show -p ActiveState --value fleet-owner-setup.service | grep -qx activating")
        assert "Username: " in owner.get_tty_text("1")

    with subtest("the first boot asks for the owner's account"):
        setup(owner, True)
        setup(plain, False)

    with subtest("the account has its name, home and groups"):
        for m, admin in [(owner, True), (plain, False)]:
            out = m.succeed("getent passwd ann").strip()
            assert ":Ann Example:" in out and ":/home/ann:" in out, out
            m.succeed("test -d /home/ann && test \"$(stat -c %U /home/ann)\" = ann")
            groups = m.succeed("id -nG ann").split()
            assert ("wheel" in groups) == admin, groups
            assert ("networkmanager" in groups) == admin, groups
            assert "ssh-users" in groups and "fleet-local" in groups, groups
            assert ("network-users" in groups) != admin, groups
            assert m.succeed("cat /var/lib/fleet/owner").strip() == "ann"
            m.succeed("systemctl is-active getty@tty1.service")
            m.fail("systemctl is-active fleet-owner-setup.service")
            # A fleet account stays what the configuration says.
            assert "ann" not in m.succeed("getent passwd bob")

    with subtest("the owner logs in at the console"):
        owner.send_chars("ann\n")
        owner.wait_until_tty_matches("1", "Password")
        owner.send_chars(password + "\n")
        owner.wait_until_tty_matches("1", "ann@")
        owner.send_chars("id\n")
        owner.wait_until_tty_matches("1", "wheel")

    with subtest("activating again keeps the account, its password and groups"):
        for m in [owner, plain]:
            before = m.succeed("getent shadow ann; id -nG ann").strip()
            marker = m.succeed("stat -c %Y /var/lib/fleet/owner").strip()
            m.succeed("/run/current-system/bin/switch-to-configuration test")
            assert m.succeed("getent shadow ann; id -nG ann").strip() == before
            m.succeed("systemctl start fleet-owner-setup.service")
            m.fail("systemctl is-active fleet-owner-setup.service")
            assert m.succeed("cat /var/lib/fleet/owner").strip() == "ann"
            assert m.succeed("stat -c %Y /var/lib/fleet/owner").strip() == marker
            # The condition skipped it: nothing is on the console but the login.
            m.succeed("systemctl is-active getty@tty1.service")

    with subtest("a reboot shows the login prompt, not the setup"):
        for m in [owner, plain]:
            m.shutdown()
            m.start()
            m.wait_for_unit("multi-user.target")
            m.wait_until_tty_matches("1", "login: ")
            assert "Username: " not in m.get_tty_text("1")
            assert m.succeed("cat /var/lib/fleet/owner").strip() == "ann"
            m.succeed("getent passwd ann")

    with subtest("her own authorized_keys admits her over SSH, and not bob's"):
        owner.succeed(
            "install -d -m 0700 -o ann -g users /home/ann/.ssh",
            "install -m 0600 -o ann -g users ${keys}/owner.pub /home/ann/.ssh/authorized_keys",
            "install -m 0600 ${keys}/owner /root/key",
        )
        ssh = "ssh -i /root/key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes"
        owner.wait_for_unit("sshd.service")
        assert owner.succeed(f"{ssh} ann@localhost id -un").strip() == "ann"
        owner.succeed(
            "install -d -m 0700 -o bob -g users /home/bob/.ssh",
            "install -m 0600 -o bob -g users ${keys}/owner.pub /home/bob/.ssh/authorized_keys",
        )
        owner.fail(f"{ssh} bob@localhost true")

    with subtest("an unprivileged owner has no sudo and a narrow NetworkManager"):
        plain.fail("runuser -u ann -- sudo -n true")
        perms = plain.succeed("runuser -u ann -- nmcli general permissions")
        print(perms)
        import re
        assert re.search(r"org\.freedesktop\.NetworkManager\.enable-disable-wifi\s+yes", perms), perms
        assert re.search(r"org\.freedesktop\.NetworkManager\.settings\.modify\.system\s+(no|auth)", perms), perms

    with subtest("a privileged owner has sudo with her password"):
        owner.succeed(f"runuser -u ann -- sh -c 'echo \"{password}\" | sudo -S true'")
  '';
}
