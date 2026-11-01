# Test of `fleetmgr host adopt` on a NixOS machine the inventory does not
# have: laptop3, installed and configured by hand, with an administrator,
# alice, whose sudo asks for her password, and an ordinary account, bob. The
# forge node plays the maintainer's machine and serves the inventory and the
# secrets over SSH; a stub nix answers what the command asks of the
# inventory's flake, as the import test has it (the flake check adopt-golden
# evaluates what the generator writes for real). The checks:
# - fleetmgr records laptop3's host key only with the fingerprint from its
#   console, and takes alice's sudo password from stdin without showing it;
# - from a skeleton: the folder holds a default.nix written from what the
#   machine is (its accounts, GRUB on its disk, its release), its hardware
#   configuration, and its /etc/nixos in previous/; it is committed signed,
#   pushed, and the host's branch published;
# - with its configuration copied: an evaluation that fails stops with a
#   hint and leaves the folder uncommitted; the next run checks it again,
#   writes nothing, and goes on to commit and publish;
# - the machine is then brought under the fleet: its identity and its branch
#   of the secrets, alice's password hash and key and support's keys stored
#   there, and a trial switch confirmed as support. Afterwards alice logs in
#   with her key and her sudo takes her password, both from the secrets.
#   The VM can neither fetch fleet-manager nor build a configuration, so
#   laptop3 has the secrets tool already (and so moves from another
#   repository), and the switch is to a stand-in flake whose system
#   activates laptop3's specialisation `fleet`: the fleet's modules, built
#   from tests/fleet/adopt-host. Its first-boot clone is off, so the run
#   stops at /etc/nixos, as the enroll test's does.
# Run it with `nix build .#checks.x86_64-linux.fleet-adopt-machine`.
{ pkgs, self, inputs }:
let
  lib = pkgs.lib;
  system = pkgs.stdenv.hostPlatform.system;
  fleetmgr = self.packages.${system}.fleetmgr;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  fleetNode = mkHost.modulesFor { hostName = "laptop3"; hostDir = ./adopt-host; };

  inventoryUrl = "ssh://git@forge/var/lib/forge/inventory.git";
  secretsUrl = "ssh://git@forge/var/lib/forge/secrets.git";
  # mkpasswd -m sha-512 -S adoptsalt alice-sudo-pw
  aliceHash = "$6$adoptsalt$Q9sCZ68fkgsxrmbmONGbPR7w.2YSl/b6bV4nHt0wOqdFLyC6WPxyIoUX9gVqKJe79GLPNkm3v7MkjZIqE/X5F/";

  fixtures = pkgs.runCommand "fleet-adopt-fixtures" {
    nativeBuildInputs = with pkgs; [ openssh age git ];
  } ''
    export HOME="$TMPDIR/home"
    mkdir -p "$HOME" $out
    for key in signing admin support; do
      ssh-keygen -q -t ed25519 -N "" -C "$key@test" -f "$out/$key"
    done
    echo "maintainer@test namespaces=\"git\" $(cut -d' ' -f1,2 "$out/signing.pub")" > $out/allowed_signers
    age-keygen -o "$out/recovery.key" 2>/dev/null
    age-keygen -y "$out/recovery.key" > "$out/recovery.pub"
    # A secrets repository as fleetmgr-secrets starts: main holds a README.
    git init -q --bare -b main "$out/secrets.git"
    git init -q -b main "$TMPDIR/secrets"
    (
      cd "$TMPDIR/secrets"
      echo "# Secrets" > README.md
      printf '*.enroll\n' > .gitignore
      git add -A
      git -c gpg.format=ssh -c "user.signingkey=$out/signing" -c commit.gpgsign=true \
        -c user.name=maintainer -c user.email=maintainer@test commit -q -m "fleetmgr-secrets, empty"
      git push -q "$out/secrets.git" main
    )
  '';

  # What fleetmgr asks of the inventory's flake, answered from its files.
  stubNix = pkgs.writeShellScript "nix" ''
    hosts=$INV/config/hosts
    field() { grep -h "$1" "$hosts/$2/default.nix" | head -1 | sed 's/.*= \(".*"\);.*/\1/'; }
    # The host an attribute path names: nixosConfigurations.<host>.config…
    all="$*"; h=''${all#*nixosConfigurations.}; h=''${h%%.*}
    case "$all" in
      # The stand-in flake is evaluated for real.
      *"/srv/fleet#"*) exec ${pkgs.nix}/bin/nix "$@" ;;
      # And a flake that is not there is not.
      *"/srv/none#"*) echo "error: path '/srv/none' does not exist" >&2; exit 1 ;;
      *"#fleetLayout"*) echo '{"hostBranches":true,"hosts":"config/hosts","users":"config/users","shared":[]}' ;;
      *"config.conf.fleet.personal.enable else null"*) echo null ;;
      *"c.config.conf.fleet.repo.url) cs"*) echo '{"alpha":"${inventoryUrl}"}' ;;
      *"c.config.conf.secrets.repo.url) cs"*) echo '{"alpha":"${secretsUrl}"}' ;;
      *darwinConfigurations*) echo '{}' ;;
      *"attrNames c.config.conf.users.accounts"*) echo '{"alpha":[],"laptop3":["alice","bob"],"laptop3-skeleton-only":["alice","bob"],"laptop4":["alice","bob"]}' ;;
      *"config.conf.users.accounts --apply"*)
        echo '{"alice":{"password":true,"keys":true},"bob":{"password":false,"keys":false},"support":{"password":false,"keys":true}}' ;;
      *"toplevel.drvPath) cs"*)
        if [ -e "$HOME/fail-eval" ]; then
          echo "error: The option \`networking.hostName' has conflicting definition values:" >&2
          exit 1
        fi
        echo '{"alpha":"/nix/store/alpha.drv","laptop3":"/nix/store/laptop3.drv","laptop3-skeleton-only":"/nix/store/s.drv","laptop4":"/nix/store/laptop4.drv"}' ;;
      *"path:"*"toplevel.drvPath"*) echo /nix/store/branch.drv ;;
      *"nixosConfigurations."*".config.conf.fleet.repo.url"*) field repo.url "$h" ;;
      *"nixosConfigurations."*".config.conf.fleet.repo.ref"*) echo "\"host/$h\"" ;;
      *"allowedSignersFile"*) cat "$INV/fleet/allowed_signers" ;;
      *"nixosConfigurations."*".config.conf.secrets.repo.url"*) field secrets.repo.url "$h" ;;
      *) echo "stub nix: not expected: $*" >&2; exit 1 ;;
    esac
  '';

  # laptop3's /etc/nixos, as its owner keeps it; hardware-configuration.nix
  # is the machine's own, made by nixos-generate-config in the test.
  configuration = pkgs.writeText "configuration.nix" ''
    { config, pkgs, ... }:
    {
      imports = [ ./hardware-configuration.nix ];
      boot.loader.grub.device = "/dev/vda";
      networking.hostName = "laptop3";
      time.timeZone = "Europe/Berlin";
      users.users.alice = { isNormalUser = true; extraGroups = [ "wheel" "audio" ]; };
      users.users.bob.isNormalUser = true;
      services.openssh.enable = true;
      system.stateVersion = "24.11";
    }
  '';

  # laptop3's configuration as the switch's steps read it, with a system that
  # activates its specialisation `fleet` (whose path the test puts in
  # /srv/fleet-system): the same on both nodes.
  standInFlake = pkgs.writeText "flake.nix" ''
    {
      outputs = _: {
        nixosConfigurations.laptop3.config = {
          conf.fleet.repo.url = "${inventoryUrl}";
          conf.fleet.signing.allowedSignersFile = ./allowed_signers;
          conf.secrets.repo.url = "${secretsUrl}";
          conf.secrets.repo.ref = "host/laptop3";
          conf.secrets.statePath = "/var/lib/fleet";
          conf.hardware.disk.enable = false;
          fileSystems = { };
          boot.loader.systemd-boot.enable = false;
          boot.loader.grub.enable = false;
          boot.loader.grub.efiSupport = false;
          sops.secrets = { };
          system.build.toplevel = ./system;
        };
      };
    }
  '';
  standInSwitch = pkgs.writeScript "switch-to-configuration" ''
    #!/bin/sh
    c=${pkgs.coreutils}/bin
    here=$("$c/dirname" "$("$c/dirname" "$("$c/readlink" -f "$0")")")
    case "$1" in test|switch) ;; *) exit 0 ;; esac
    "$("$c/cat" /srv/fleet-system)/bin/switch-to-configuration" test || exit
    "$c/ln" -sfn "$here" /run/current-system
  '';
in
pkgs.testers.runNixOSTest {
  name = "fleet-adopt-machine";

  node.specialArgs = fleetNode.specialArgs;
  node.pkgsReadOnly = false;
  # The test network has a fixed address and no DHCP. NetworkManager, which
  # fleet hosts run, would otherwise try DHCP on it when it sees the interface
  # before the address, give up after a while and take the interface down: a
  # slow boot then cut the host off in the middle of the test.
  defaults.networking.networkmanager.unmanaged = [ "eth1" ];

  nodes.laptop3 = {
    services.openssh.enable = true;
    time.timeZone = "Europe/Berlin";
    # As on a machine set up by hand: alice's sudo asks for her password, as
    # NixOS has it by default.
    users.users.alice = {
      isNormalUser = true;
      description = "Alice";
      extraGroups = [ "wheel" "audio" ];
      hashedPassword = aliceHash;
      openssh.authorizedKeys.keyFiles = [ "${fixtures}/admin.pub" ];
    };
    users.users.bob = { isNormalUser = true; description = "Bob"; };
    # The VM cannot fetch fleet-manager to run its init, so it has the tool.
    environment.systemPackages = [ self.packages.${system}.fleetmgr-secrets-sync pkgs.git ];
    # Going back activates the running system again, which tests leave out.
    system.switch.enable = true;

    # laptop3 as the fleet runs it, which the stand-in's system activates.
    specialisation.fleet.configuration = {
      imports = fleetNode.modules;
      boot.loader.grub.enable = lib.mkForce false;
      # What a host with secrets refuses, from the machine's own settings, as
      # a copied configuration has it put aside.
      users.users.alice.hashedPassword = lib.mkForce null;
      users.users.alice.openssh.authorizedKeys.keyFiles = lib.mkForce [ ];
      conf.fleet = {
        repo.url = inventoryUrl;
        signing.allowedSignersFile = "${fixtures}/allowed_signers";
        support.keysFromSecrets = true;
      };
      conf.secrets.repo.url = secretsUrl;
      # No first-boot clone or timer: the test cannot build what they fetch.
      systemd.services.fleetmgr-bootstrap.enable = false;
      systemd.timers.config-auto-update.enable = false;
    };
  };

  nodes.forge = { pkgs, ... }: {
    services.openssh.enable = true;
    users.users.git.isNormalUser = true;
    environment.systemPackages = [ pkgs.git fleetmgr ];
    nix.settings.experimental-features = [ "nix-command" "flakes" ];
  };

  testScript = ''
    start_all()
    laptop3.wait_for_unit("sshd.service")
    forge.wait_for_unit("sshd.service")

    # laptop3 as its owner set it up: a configuration in /etc/nixos, and GRUB
    # on its disk (a test VM boots without a loader, so /boot/grub stands in).
    laptop3.succeed(
        "mkdir -p /etc/nixos /boot/grub",
        "cp ${configuration} /etc/nixos/configuration.nix",
        "nixos-generate-config --show-hardware-config > /etc/nixos/hardware-configuration.nix",
        # A checkout of another repository, which with the secrets tool it has
        # makes it a host that moves (see above); its .git is not copied.
        "git -C /etc/nixos init -q && git -C /etc/nixos remote add origin https://example.org/laptop3.git",
    )
    print_ = laptop3.succeed("ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub").split()[1]

    # The maintainer: keys, a signing identity, the inventory, the secrets.
    forge.succeed(
        "install -d -o git /home/git/bin /home/git/.ssh /home/git/.local/state/fleet",
        "install -o git -m 600 ${fixtures}/admin ${fixtures}/signing ${fixtures}/recovery.key /home/git/",
        "install -o git -m 644 ${fixtures}/support.pub /home/git/",
        "install -o git -m 600 ${fixtures}/support /home/git/.ssh/id_ed25519",
        "install -o git -m 644 ${fixtures}/recovery.pub /home/git/.local/state/fleet/recovery.pub",
        "install -o git -m 755 ${stubNix} /home/git/bin/nix",
        "touch /home/git/.ssh/authorized_keys && chown git /home/git/.ssh/authorized_keys && chmod 600 /home/git/.ssh/authorized_keys",
        "mkdir -p /var/lib/forge && git init -q --bare -b main /var/lib/forge/inventory.git"
        " && cp -r --no-preserve=mode ${fixtures}/secrets.git /var/lib/forge/ && chown -R git /var/lib/forge",
        "su git -c 'git config --global user.name maintainer && git config --global user.email maintainer@test"
        " && git config --global gpg.format ssh && git config --global user.signingKey /home/git/signing"
        " && git config --global commit.gpgSign true && git config --global init.defaultBranch main'",
        "su git -c 'git clone -q file:///var/lib/forge/inventory.git /home/git/inv 2>/dev/null"
        " && cd /home/git/inv && mkdir -p fleet config/hosts/alpha && cp ${fixtures}/allowed_signers fleet/"
        " && echo \"{ }\" > flake.nix && echo \"{ }\" > flake.lock && echo alpha > config/hosts/alpha/default.nix"
        " && git add -A && git commit -q -m inventory && git push -q origin main'",
        "su git -c 'git clone -q file:///var/lib/forge/secrets.git /home/git/secrets"
        " && git -C /home/git/secrets config gpg.ssh.allowedSignersFile ${fixtures}/allowed_signers'",
        "cd /home/git && su git -c 'PATH=/home/git/bin:$PATH INV=/home/git/inv fleetmgr trust signers --flake /home/git/inv --yes'",
    )

    def adopt(args, name="laptop3"):
        status, out = forge.execute(
            "cd /home/git && echo alice-sudo-pw | timeout 900 su git -c 'PATH=/home/git/bin:$PATH INV=/home/git/inv"
            " SOPS_AGE_KEY_FILE=/home/git/recovery.key"
            f" fleetmgr host adopt {name} --address laptop3 --flake /home/git/inv --repo /home/git/secrets --recovery \"\""
            f" --admin alice --key /home/git/admin --admin-password-stdin --yes {args}' 2>&1"
        )
        print(out)
        return status, out

    def files(branch, repo="inventory"):
        return forge.succeed(f"su git -c 'git -C /var/lib/forge/{repo}.git ls-tree -r --name-only {branch}'").split()

    def verified(ref):
        forge.succeed(f"su git -c 'git -C /var/lib/forge/inventory.git -c gpg.ssh.allowedSignersFile=${fixtures}/allowed_signers verify-commit {ref}'")

    # The first two runs write folders under other names, and stop where the
    # switch is chosen, at a configuration that is not there (/srv/none),
    # before anything is made on the machine: what they wrote is what they
    # check.
    with subtest("a machine is adopted from a skeleton, with its host key compared and its admin's sudo password"):
        status, out = adopt(f"--skeleton --grub-device /dev/vda --host-key {print_} --sync=false --switch-to /srv/none",
                            name="laptop3-skeleton-only")
        for want in ["it matches the fingerprint given with --host-key", "alice's sudo takes its password",
                     "alice, an administrator: a password hash (not shown), 1 login key(s)",
                     "bob, an ordinary account: no password, no login keys",
                     "committed: feat(hosts): laptop3-skeleton-only, adopted from the machine as it ran"]:
            assert want in out, f"output lacks {want!r}"
        assert "alice-sudo-pw" not in out, "the sudo password was shown"
        forge.succeed("grep -q '^laptop3 ssh-ed25519 ' /home/git/.ssh/known_hosts")
        d = forge.succeed("su git -c 'git -C /var/lib/forge/inventory.git show main:config/hosts/laptop3-skeleton-only/default.nix'")
        for want in ['system.stateVersion = "24.11";', 'boot.loader.grub.device = "/dev/vda";',
                     'users.accounts.alice = {', 'privileged = true;', 'groups = [ "audio" ];',
                     'secrets.password = true;', 'secrets.authorizedKeys = true;',
                     'users.accounts.bob = {', 'secrets.password = false;',
                     'machineType = "vm";', 'timezone = "Europe/Berlin";',
                     'repo.url = "${inventoryUrl}";', 'secrets.repo.url = "${secretsUrl}";',
                     'signing.allowedSignersFile = ../../../fleet/allowed_signers;']:
            assert want in d, f"default.nix lacks {want!r}:\n{d}"
        main = files("main")
        for f in ["default.nix", "hardware-configuration.nix", "previous/configuration.nix", "previous/hardware-configuration.nix"]:
            assert f"config/hosts/laptop3-skeleton-only/{f}" in main, f"main lacks {f}: {main}"
        verified("main")
        assert "config/hosts/laptop3-skeleton-only/default.nix" in files("host/laptop3-skeleton-only")
        verified("host/laptop3-skeleton-only")

    with subtest("a copied configuration that does not evaluate stays for the maintainer, and the next run goes on from it"):
        forge.succeed("su git -c 'touch /home/git/fail-eval'")
        status, out = adopt("--copy-config --sync=false --switch-to /srv/none", name="laptop4")
        assert status != 0, "went on although the host does not evaluate"
        for want in ["Hint: the configuration in previous/ and the fleet's modules both set that option",
                     "The folder config/hosts/laptop4 stays as it is"]:
            assert want in out, f"output lacks {want!r}"
        forge.succeed("test -e /home/git/inv/config/hosts/laptop4/previous/configuration.nix")
        assert "config/hosts/laptop4/default.nix" not in files("main")
        d = forge.succeed("cat /home/git/inv/config/hosts/laptop4/default.nix")
        for want in ["imports = [ ./previous/configuration.nix ];", "hardware.boot.loader = null;",
                     "openssh.authorizedKeys.keys = lib.mkForce [ ];", "hashedPassword = lib.mkForce null;"]:
            assert want in d, f"default.nix lacks {want!r}:\n{d}"
        forge.succeed("rm /home/git/fail-eval")
        status, out = adopt("--copy-config --sync=false --switch-to /srv/none", name="laptop4")
        assert "an earlier run wrote its folder, which is checked again as it is" in out, out
        assert "config/hosts/laptop4/default.nix" in files("main")
        verified("main")
        assert "config/hosts/laptop4/previous/configuration.nix" in files("host/laptop4")

    # The stand-in flake, on both nodes.
    for node in (forge, laptop3):
        node.succeed(
            "mkdir -p /srv/fleet/system/bin && cp ${standInFlake} /srv/fleet/flake.nix && cp ${fixtures}/allowed_signers /srv/fleet/",
            "echo '{\"nodes\":{\"root\":{}},\"root\":\"root\",\"version\":7}' > /srv/fleet/flake.lock",
            "install -m 0755 ${standInSwitch} /srv/fleet/system/bin/switch-to-configuration",
        )
    laptop3.succeed(
        "readlink -f /run/current-system/specialisation/fleet > /srv/fleet-system",
        # The rest of the fleet system, for the comparison of services and
        # for the test driver.
        "for f in \"$(cat /srv/fleet-system)\"/*; do [ \"''${f##*/}\" = bin ] || ln -s \"$f\" /srv/fleet/system/; done",
    )
    before = laptop3.succeed("readlink -f /run/current-system").strip()

    with subtest("the machine is brought under the fleet, its accounts keeping their passwords and keys"):
        status, out = adopt("--skeleton --grub-device /dev/vda --switch-to /srv/fleet --trial-minutes 2 --sync=false"
                            " --support-keys /home/git/support.pub --register-with \"cat >> /home/git/.ssh/authorized_keys\"")
        for want in ["created under /var/lib/fleet", "committed: host-enroll: laptop3",
                     "fetched, verified and decrypted", "List what stops running on laptop3",
                     "stopped the timer: the new system stays", "the new system is the boot default"]:
            assert want in out, f"output lacks {want!r}"
        assert "alice-sudo-pw" not in out, "the sudo password was shown"
        # It has no first-boot clone to make /etc/nixos the checkout with.
        assert status != 0 and "Make /etc/nixos the configuration" in out.split("stopped at step")[-1], out
        secrets = files("host/laptop3", repo="secrets")
        for f in ["hosts/laptop3.yaml", "users/alice/laptop3.yaml", "users/support/laptop3.yaml"]:
            assert f in secrets, f"host/laptop3 of the secrets lacks {f}: {secrets}"
        assert "users/bob/laptop3.yaml" not in secrets, "bob has nothing to store"
        assert laptop3.succeed("readlink -f /run/current-system").strip() != before
        laptop3.succeed("id support", "test -s /run/secrets/users/alice/authorized_keys")
        laptop3.fail("systemctl is-active fleetmgr-trial-rollback.timer")
        # alice, as the fleet runs the machine: her key and her password, from the secrets.
        forge.succeed(
            "su git -c 'ssh -o BatchMode=yes -o IdentitiesOnly=yes -i /home/git/admin alice@laptop3"
            " \"echo alice-sudo-pw | sudo -S -p \\\"\\\" true\"'"
        )
        forge.succeed("su git -c 'ssh -o BatchMode=yes support@laptop3 sudo -n true'")
  '';
}
