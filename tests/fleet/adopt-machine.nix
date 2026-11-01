# Test of `fleetmgr host adopt` on a NixOS machine the inventory does not
# have: laptop3, installed and configured by hand, with an administrator,
# alice, whose sudo asks for her password, and an ordinary account, bob. The
# forge node plays the maintainer's machine and serves the inventory; a stub
# nix answers what the command asks of the inventory's flake, as the import
# test has it (the flake check adopt-golden evaluates what the generator
# writes for real). The checks:
# - fleetmgr records laptop3's host key only with the fingerprint from its
#   console, and takes alice's sudo password from stdin without showing it;
# - from a skeleton: the folder holds a default.nix written from what the
#   machine is (its accounts, GRUB on its disk, its release), its hardware
#   configuration, and its /etc/nixos in previous/; it is committed signed,
#   pushed, and the host's branch published;
# - with its configuration copied: an evaluation that fails stops with a
#   hint and leaves the folder uncommitted; the next run checks it again,
#   writes nothing, and goes on to commit and publish.
# Run it with `nix build .#checks.x86_64-linux.fleet-adopt-machine`.
{ pkgs, self }:
let
  lib = pkgs.lib;
  fleetmgr = self.packages.${pkgs.stdenv.hostPlatform.system}.fleetmgr;

  fixtures = pkgs.runCommand "fleet-adopt-fixtures" { nativeBuildInputs = with pkgs; [ openssh mkpasswd ]; } ''
    mkdir $out
    for key in signing admin; do
      ssh-keygen -q -t ed25519 -N "" -C "$key@test" -f "$out/$key"
    done
    echo "maintainer@test namespaces=\"git\" $(cut -d' ' -f1,2 "$out/signing.pub")" > $out/allowed_signers
    mkpasswd -m sha-512 -S adoptsalt alice-sudo-pw > $out/alice.hash
  '';

  # What fleetmgr asks of the inventory's flake, answered from its files.
  stubNix = pkgs.writeShellScript "nix" ''
    hosts=$INV/config/hosts
    field() { grep -h "$1" "$hosts/$2/default.nix" | head -1 | sed 's/.*= \(".*"\);.*/\1/'; }
    # The host an attribute path names: nixosConfigurations.<host>.config…
    all="$*"; h=''${all#*nixosConfigurations.}; h=''${h%%.*}
    case "$all" in
      *"#fleetLayout"*) echo '{"hostBranches":true,"hosts":"config/hosts","users":"config/users","shared":[]}' ;;
      *"config.conf.fleet.personal.enable else null"*) echo null ;;
      *"c.config.conf.fleet.repo.url) cs"*) echo '{"alpha":"ssh://git@forge/inventory.git"}' ;;
      *"c.config.conf.secrets.repo.url) cs"*) echo '{"alpha":"ssh://git@forge/secrets.git"}' ;;
      *darwinConfigurations*) echo '{}' ;;
      *"attrNames c.config.conf.users.accounts"*) echo '{"alpha":[],"laptop3":["alice","bob"],"laptop4":["alice","bob"]}' ;;
      *"toplevel.drvPath) cs"*)
        if [ -e "$HOME/fail-eval" ]; then
          echo "error: The option \`networking.hostName' has conflicting definition values:" >&2
          exit 1
        fi
        echo '{"alpha":"/nix/store/alpha.drv","laptop3":"/nix/store/laptop3.drv","laptop4":"/nix/store/laptop4.drv"}' ;;
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
in
pkgs.testers.runNixOSTest {
  name = "fleet-adopt-machine";

  nodes.laptop3 = {
    services.openssh.enable = true;
    time.timeZone = "Europe/Berlin";
    users.users.alice = {
      isNormalUser = true;
      description = "Alice";
      extraGroups = [ "wheel" "audio" ];
      hashedPasswordFile = "${fixtures}/alice.hash";
      openssh.authorizedKeys.keyFiles = [ "${fixtures}/admin.pub" ];
    };
    users.users.bob = { isNormalUser = true; description = "Bob"; };
    # As on a machine set up by hand: sudo asks wheel for the password.
    security.sudo.wheelNeedsPassword = true;
  };

  nodes.forge = { pkgs, ... }: {
    users.users.git.isNormalUser = true;
    environment.systemPackages = [ pkgs.git fleetmgr ];
  };

  testScript = ''
    start_all()
    laptop3.wait_for_unit("sshd.service")
    forge.wait_for_unit("multi-user.target")

    # laptop3 as its owner set it up: a configuration in /etc/nixos, and GRUB
    # on its disk (a test VM boots without a loader, so /boot/grub stands in).
    laptop3.succeed(
        "mkdir -p /etc/nixos /boot/grub",
        "cp ${configuration} /etc/nixos/configuration.nix",
        "nixos-generate-config --show-hardware-config > /etc/nixos/hardware-configuration.nix",
    )
    print_ = laptop3.succeed("ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub").split()[1]

    # The maintainer: keys, a signing identity, the inventory and its trust.
    forge.succeed(
        "install -d -o git /home/git/bin /home/git/.ssh",
        "install -o git -m 600 ${fixtures}/admin ${fixtures}/signing /home/git/",
        "install -o git -m 755 ${stubNix} /home/git/bin/nix",
        "git init -q --bare -b main /var/lib/forge/inventory.git && chown -R git /var/lib/forge",
        "su git -c 'git config --global user.name maintainer && git config --global user.email maintainer@test"
        " && git config --global gpg.format ssh && git config --global user.signingKey /home/git/signing"
        " && git config --global commit.gpgSign true && git config --global init.defaultBranch main'",
        "su git -c 'git clone -q file:///var/lib/forge/inventory.git /home/git/inv 2>/dev/null"
        " && cd /home/git/inv && mkdir -p fleet config/hosts/alpha && cp ${fixtures}/allowed_signers fleet/"
        " && echo \"{ }\" > flake.nix && echo \"{ }\" > flake.lock && echo alpha > config/hosts/alpha/default.nix"
        " && git add -A && git commit -q -m inventory && git push -q origin main'",
        "su git -c 'git init -q /home/git/secrets'",
        "cd /home/git && su git -c 'PATH=/home/git/bin:$PATH INV=/home/git/inv fleetmgr trust signers --flake /home/git/inv --yes'",
    )

    def adopt(args, name="laptop3"):
        status, out = forge.execute(
            "cd /home/git && echo alice-sudo-pw | timeout 300 su git -c 'PATH=/home/git/bin:$PATH INV=/home/git/inv"
            f" fleetmgr host adopt {name} --address laptop3 --flake /home/git/inv --repo /home/git/secrets"
            f" --admin alice --key /home/git/admin --admin-password-stdin --yes {args}' 2>&1"
        )
        print(out)
        return status, out

    def files(branch):
        return forge.succeed(f"su git -c 'git -C /var/lib/forge/inventory.git ls-tree -r --name-only {branch}'").split()

    def verified(ref):
        forge.succeed(f"su git -c 'git -C /var/lib/forge/inventory.git -c gpg.ssh.allowedSignersFile=${fixtures}/allowed_signers verify-commit {ref}'")

    with subtest("a machine is adopted from a skeleton, with its host key compared and its admin's sudo password"):
        status, out = adopt(f"--skeleton --grub-device /dev/vda --host-key {print_}")
        assert status == 0, out
        assert "alice-sudo-pw" not in out, "the sudo password was shown"
        for want in ["it matches the fingerprint given with --host-key", "alice's sudo takes its password",
                     "alice, an administrator: a password hash (not shown), 1 login key(s)",
                     "bob, an ordinary account: no password, no login keys",
                     "committed: feat(hosts): laptop3, adopted from the machine as it ran"]:
            assert want in out, f"output lacks {want!r}"
        forge.succeed("grep -q '^laptop3 ssh-ed25519 ' /home/git/.ssh/known_hosts")
        d = forge.succeed("su git -c 'git -C /var/lib/forge/inventory.git show main:config/hosts/laptop3/default.nix'")
        for want in ['system.stateVersion = "24.11";', 'boot.loader.grub.device = "/dev/vda";',
                     'users.accounts.alice = {', 'privileged = true;', 'groups = [ "audio" ];',
                     'secrets.password = true;', 'secrets.authorizedKeys = true;',
                     'users.accounts.bob = {', 'secrets.password = false;',
                     'machineType = "vm";', 'timezone = "Europe/Berlin";',
                     'repo.url = "ssh://git@forge/inventory.git";', 'secrets.repo.url = "ssh://git@forge/secrets.git";',
                     'signing.allowedSignersFile = ../../../fleet/allowed_signers;']:
            assert want in d, f"default.nix lacks {want!r}:\n{d}"
        main = files("main")
        for f in ["config/hosts/laptop3/default.nix", "config/hosts/laptop3/hardware-configuration.nix",
                  "config/hosts/laptop3/previous/configuration.nix", "config/hosts/laptop3/previous/hardware-configuration.nix"]:
            assert f in main, f"main lacks {f}: {main}"
        verified("main")
        assert "config/hosts/laptop3/default.nix" in files("host/laptop3")
        verified("host/laptop3")
        assert forge.succeed("su git -c 'git -C /home/git/inv status --porcelain'").strip() == ""

    with subtest("a copied configuration that does not evaluate stays for the maintainer, and the next run goes on from it"):
        forge.succeed("su git -c 'touch /home/git/fail-eval'")
        status, out = adopt("--copy-config", name="laptop4")
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
        status, out = adopt("--copy-config", name="laptop4")
        assert status == 0, out
        assert "an earlier run wrote its folder, which is checked again as it is" in out, out
        assert "config/hosts/laptop4/default.nix" in files("main")
        verified("main")
        assert "config/hosts/laptop4/previous/configuration.nix" in files("host/laptop4")
  '';
}
