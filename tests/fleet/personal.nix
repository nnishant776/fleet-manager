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
#   actions; on `owner` she has sudo with her password;
# - adoption (what the inventory holds after `fleet host adopt`): on `owner`,
#   a switch to a configuration that declares her, with no uid, and turns
#   personal mode off keeps her uid, her files, her password and her
#   groups, makes accounts immutable, removes the setup service and the
#   fleet-local group, and stops her home keys from admitting her.
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
    }
    # What the inventory holds once `fleet host adopt` has run: the owner
    # declared with no uid, personal mode off. NixOS keeps the uid she has. The password comes
    # from a file the test writes, in place of the secrets this host has none
    # of. Only the administrator's host has it.
    (lib.optionalAttrs ownerPrivileged {
      specialisation.adopted.configuration = {
        conf.fleet.personal.enable = lib.mkForce false;
        conf.users.accounts.ann = {
          fullName = "Ann Example";
          privileged = true;
        };
        users.users.ann.hashedPasswordFile = "/var/lib/adopt/ann-hash";
      };
    })];
  };

  # The uid the first account the setup makes gets after bob's; the test
  # asserts it before the switch and again after it, which shows that NixOS
  # keeps it by itself: the declaration gives none.
  annUid = 1001;
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
        ssh = "ssh -n -i /root/key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes"
        owner.wait_for_unit("sshd.service")
        assert owner.succeed(f"{ssh} ann@localhost id -un", timeout=60).strip() == "ann"

        def refused(user):
            # ssh's own refusal: 255 and "Permission denied", not a timeout,
            # which fail() would take for a refusal too.
            status, out = owner.execute(f"{ssh} {user}@localhost true 2>&1", timeout=60)
            assert status == 255 and "Permission denied" in out, (status, out)

        owner.succeed(
            "install -d -m 0700 -o bob -g users /home/bob/.ssh",
            "install -m 0600 -o bob -g users ${keys}/owner.pub /home/bob/.ssh/authorized_keys",
        )
        refused("bob")

    with subtest("an unprivileged owner has no sudo and a narrow NetworkManager"):
        plain.fail("runuser -u ann -- sudo -n true")
        perms = plain.succeed("runuser -u ann -- nmcli general permissions")
        print(perms)
        import re
        assert re.search(r"org\.freedesktop\.NetworkManager\.enable-disable-wifi\s+yes", perms), perms
        assert re.search(r"org\.freedesktop\.NetworkManager\.settings\.modify\.system\s+(no|auth)", perms), perms

    with subtest("a privileged owner has sudo with her password"):
        owner.succeed(f"runuser -u ann -- sh -c 'echo \"{password}\" | sudo -S true'")

    with subtest("adopting the owner keeps her uid, files and password under immutable accounts"):
        uid = "${toString annUid}"
        assert owner.succeed("id -u ann").strip() == uid, owner.succeed("getent passwd ann")
        hash_before = owner.succeed("getent shadow ann | cut -d: -f2").strip()
        assert hash_before.startswith("$"), hash_before
        # What `fleet host adopt` stores in the secrets, and the host then
        # takes from there; this host has none, so a file stands in.
        owner.succeed(
            "install -d -m 0700 /var/lib/adopt",
            "getent shadow ann | cut -d: -f2 > /var/lib/adopt/ann-hash",
            "chmod 0600 /var/lib/adopt/ann-hash",
            "echo mine > /home/ann/mine && chown ann: /home/ann/mine",
            "systemctl is-active sshd.service",
        )
        assert owner.succeed("id -nG ann").split().count("fleet-local") == 1
        # An account made by hand, which the new configuration does not know.
        owner.succeed("useradd stray")
        # Her user manager outlives her last session, the SSH login above, by
        # ten seconds; home-manager switching her units while it stops fails.
        owner.wait_until_succeeds(
            f"systemctl show -p ActiveState --value user@{uid}.service | grep -qxE 'inactive|failed'",
            timeout=60,
        )
        owner.succeed("/run/current-system/specialisation/adopted/bin/switch-to-configuration test")

        # Her ids, files and password are as they were.
        assert owner.succeed("id -u ann").strip() == uid
        assert owner.succeed("stat -c %u:%G /home/ann /home/ann/mine").split() == [uid + ":users"] * 2
        assert owner.succeed("getent shadow ann | cut -d: -f2").strip() == hash_before
        assert owner.succeed("cat /home/ann/mine").strip() == "mine"
        groups = owner.succeed("id -nG ann").split()
        assert "wheel" in groups and "ssh-users" in groups, groups
        # Accounts are the configuration's now: the stray one is gone.
        owner.fail("getent passwd stray")
        owner.succeed("getent passwd ann bob")
        # She has not become less than she was, and the setup is gone with the
        # group that let her home keys in.
        owner.fail("systemctl cat fleet-owner-setup.service")
        owner.fail("getent group fleet-local")
        assert "fleet-local" not in groups, groups
        refused("ann")
        owner.succeed(f"runuser -u ann -- sh -c 'echo \"{password}\" | sudo -S true'")

        # The same password at the console, on a terminal of its own.
        owner.send_key("alt-f2")
        owner.wait_until_tty_matches("2", "login: ")
        owner.send_chars("ann\n")
        owner.wait_until_tty_matches("2", "Password")
        owner.send_chars(password + "\n")
        owner.wait_until_tty_matches("2", "ann@")
        owner.send_chars("id -u\n")
        owner.wait_until_tty_matches("2", uid)

        # Another activation of the same configuration changes nothing.
        owner.succeed("/run/current-system/bin/switch-to-configuration test")
        assert owner.succeed("id -u ann").strip() == uid
        assert owner.succeed("getent shadow ann | cut -d: -f2").strip() == hash_before
  '';
}
