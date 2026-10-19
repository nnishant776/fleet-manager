# End-to-end test of the host side of the secrets design: a `forge` node
# serves signed bare repositories over sshd and a `fleet` node, built from the
# scratch-secrets host, has to bootstrap, snapshot, activate and hold up
# against a tampered repository, and take secrets the maintainer adds with
# `fleet host secret` (the forge also plays the maintainer). No network
# and no hosting service are involved. Run it with
# `nix build .#checks.x86_64-linux.secrets-sync` (see README.md).
{ pkgs, inputs, self }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostName = "scratch-secrets";

  hostNode = mkHost.modulesFor {
    inherit hostName;
    hostDir = ../../hosts/scratch-secrets;
  };

  # Everything the nodes need that depends on keys: made once at build time,
  # the same way tests/secrets/harness.nix makes it on a developer machine.
  fixtures = pkgs.runCommand "secrets-vm-fixtures" {
    nativeBuildInputs = with pkgs; [ age openssh sops git yq-go mkpasswd coreutils findutils ];
  } ''
    export HOME="$TMPDIR/home"
    mkdir -p "$HOME" "$out"
    keys="$TMPDIR/keys"
    mkdir -p "$keys"

    age-keygen -o "$keys/recovery.key" 2>/dev/null
    age-keygen -o "$keys/host.key" 2>/dev/null
    recovery="$(age-keygen -y "$keys/recovery.key")"
    recipient="$(age-keygen -y "$keys/host.key")"

    ssh-keygen -q -t ed25519 -N "" -C "fleet-deploy@${hostName}" -f "$keys/deploy_ed25519"
    ssh-keygen -q -t ed25519 -N "" -C "maintainer@test" -f "$keys/signing_ed25519"
    ssh-keygen -q -t ed25519 -N "" -C "support@test" -f "$keys/support_ed25519"
    echo "maintainer@test $(cut -d' ' -f1,2 "$keys/signing_ed25519.pub")" > "$out/allowed_signers"
    cp "$keys/support_ed25519" "$keys/support_ed25519.pub" "$keys/deploy_ed25519.pub" "$out/"
    # The maintainer's keys, for the forge to play the maintainer with fleet.
    cp "$keys/recovery.key" "$keys/signing_ed25519" "$out/"

    # The identity document that `os-install --identity` reads, for the host key and deploy key.
    {
      echo "version: 1"
      echo "host: ${hostName}"
      echo "age_key: |"
      sed 's/^/  /' "$keys/host.key"
      echo "deploy_key: |"
      sed 's/^/  /' "$keys/deploy_ed25519"
    } > "$out/identity.yaml"

    signed_git() {
      git -c gpg.format=ssh -c "user.signingkey=$keys/signing_ed25519" \
        -c commit.gpgsign=true -c user.name=maintainer -c user.email=maintainer@test "$@"
    }

    # Writes .sops.yaml from fleet.yaml: literal recipients, two rules per
    # host. Copied from tests/secrets/harness.nix.
    render_sops_yaml() {  # render_sops_yaml REPO
      local repo="$1" maintainer host rec
      maintainer="$(yq -r '.maintainer' "$repo/fleet.yaml")"
      {
        echo "creation_rules:"
        while read -r host; do
          [ -n "$host" ] || continue
          rec="$(yq -r ".hosts.\"$host\"" "$repo/fleet.yaml")"
          echo "  - path_regex: ^hosts/$host\\.yaml\$"
          echo "    key_groups: [{ age: [$maintainer, $rec] }]"
          echo "  - path_regex: ^users/[^/]+/$host\\.yaml\$"
          echo "    key_groups: [{ age: [$maintainer, $rec] }]"
        done < <(yq -r '.hosts | keys | .[]' "$repo/fleet.yaml")
      } > "$repo/.sops.yaml"
    }

    # The secrets repository: one signed commit holding both secret files.
    git init -q --bare -b main "$out/secrets.git"
    git init -q -b main "$TMPDIR/secrets"
    git -C "$TMPDIR/secrets" remote add origin "$out/secrets.git"
    (
      cd "$TMPDIR/secrets"
      printf 'maintainer: %s\nhosts:\n  ${hostName}: %s\n' "$recovery" "$recipient" > fleet.yaml
      render_sops_yaml "$PWD"
      mkdir -p hosts users/alice
      printf 'support_password: "%s"\n' "$(mkpasswd -m yescrypt -s <<<support)" > hosts/${hostName}.yaml
      ssh-keygen -q -t ed25519 -N "" -C "alice@${hostName}" -f "$keys/alice_ed25519"
      {
        printf 'password: "%s"\n' "$(mkpasswd -m yescrypt -s <<<alice)"
        echo "ssh_key: |"
        sed 's/^/  /' "$keys/alice_ed25519"
        printf 'ssh_key_pub: "%s"\n' "$(cat "$keys/alice_ed25519.pub")"
      } > users/alice/${hostName}.yaml
      sops --encrypt --in-place hosts/${hostName}.yaml
      sops --encrypt --in-place users/alice/${hostName}.yaml
      git add -A
      signed_git commit -q -S -m "Initial scratch secrets"
      git push -q origin main
    )

    # The configuration upstream: this flake's files in one signed commit.
    git init -q --bare -b main "$out/config.git"
    mkdir "$TMPDIR/config"
    cp -r --no-preserve=mode ${self}/. "$TMPDIR/config/"
    # A worktree carries a .git file that points outside the sandbox.
    rm -rf "$TMPDIR/config/.git"
    git init -q -b main "$TMPDIR/config"
    git -C "$TMPDIR/config" remote add origin "$out/config.git"
    (
      cd "$TMPDIR/config"
      git add -A
      signed_git commit -q -S -m "Configuration snapshot for the scratch host"
      git push -q origin main
    )
  '';

  secretsUrl = "ssh://git@forge/var/lib/forge/secrets.git";
  configUrl = "ssh://git@forge/var/lib/forge/config.git";
in
pkgs.testers.runNixOSTest {
  name = "secrets-sync";

  # Test-level, not per node: the host modules take flakeLib as a module
  # argument while their imports are still being resolved.
  node.specialArgs = hostNode.specialArgs;

  # The host sets nixpkgs.config itself (modules/common/nix.nix), which a
  # read-only package set would reject.
  node.pkgsReadOnly = false;

  nodes.forge = { pkgs, ... }: {
    services.openssh.enable = true;

    users.users.git = {
      isNormalUser = true;
      openssh.authorizedKeys.keys = [ (builtins.readFile "${fixtures}/deploy_ed25519.pub") ];
    };

    # sshd runs git-upload-pack through the user's login PATH; fleet is the
    # maintainer's tool.
    environment.systemPackages = [ pkgs.git self.packages.${pkgs.stdenv.hostPlatform.system}.fleet ];

    systemd.services.forge-repos = {
      description = "Put the fixture repositories where the git user serves them";
      wantedBy = [ "multi-user.target" ];
      before = [ "sshd.service" ];
      requiredBy = [ "sshd.service" ];
      serviceConfig.Type = "oneshot";
      script = ''
        mkdir -p /var/lib/forge
        cp -r --no-preserve=mode ${fixtures}/secrets.git ${fixtures}/config.git /var/lib/forge/
        chown -R git:users /var/lib/forge
      '';
    };

    # For the access check from the forge to the fleet host.
    environment.etc."test-keys/support_ed25519" = {
      source = "${fixtures}/support_ed25519";
      mode = "0600";
    };

    # The forge also plays the maintainer's machine, running fleet with the
    # maintainer's recovery and signing keys.
    environment.etc."test-keys/recovery.key" = {
      source = "${fixtures}/recovery.key";
      mode = "0600";
    };
    environment.etc."test-keys/signing_ed25519" = {
      source = "${fixtures}/signing_ed25519";
      mode = "0600";
    };
    environment.etc."test-keys/allowed_signers".source = "${fixtures}/allowed_signers";
  };

  nodes.fleet = { config, pkgs, ... }: {
    imports = hostNode.modules ++ [
      {
        conf.hardware.disk.enable = lib.mkForce false;
        disko.devices = lib.mkForce { };
        networking.hostName = lib.mkForce "fleet";
        # The test framework turns switching off unless a test asks for it;
        # step 3 runs the activation by hand.
        system.switch.enable = lib.mkForce true;
        # No ESP and no bootable disk in the test VM; the bootloader installer run by the switch
        # would fail on its mount point check.
        boot.loader.systemd-boot.enable = lib.mkForce false;
        boot.loader.grub.enable = lib.mkForce false;
        conf.secrets.repo.url = lib.mkForce secretsUrl;
        conf.fleet.repo.url = lib.mkForce configUrl;
        conf.fleet.signing.allowedSignersFile = lib.mkForce "${fixtures}/allowed_signers";
        conf.fleet.support.sshKeys = lib.mkForce [
          (lib.removeSuffix "\n" (builtins.readFile "${fixtures}/support_ed25519.pub"))
        ];
      }
    ];

    # The first-boot configuration clone has no deploy-key secret in this
    # test (that needs a host secret the fixtures do not carry), so root
    # reaches the forge with the host's deploy key and pins nothing.
    # To read back what fleet host secret stored, with the host's own key.
    environment.systemPackages = [ pkgs.sops ];

    programs.ssh.extraConfig = ''
      Host forge
        IdentityFile /var/lib/fleet/deploy_ed25519
        IdentitiesOnly yes
        StrictHostKeyChecking accept-new
    '';

    # Stands in for os-install's identity step: the keys of the fixtures
    # are put in place before fleet-bootstrap's own `init`, which then finds
    # them and changes nothing.
    systemd.services.secrets-test-seed = {
      description = "Install the fixture identity into the state directory";
      wantedBy = [ "multi-user.target" ];
      before = [ "fleet-bootstrap.service" ];
      # A switch starts every wanted oneshot that is inactive; the identity
      # is seeded once, and a second `init` would refuse to overwrite it.
      unitConfig.ConditionPathExists = "!/var/lib/fleet/age.key";
      requiredBy = [ "fleet-bootstrap.service" ];
      serviceConfig.Type = "oneshot";
      script = ''
        ${lib.getExe (import ../../lib/secrets-sync.nix {
          inherit pkgs;
          nix = config.nix.package;
        }).sync} init --state /var/lib/fleet --host ${hostName} \
          --from-identity ${fixtures}/identity.yaml
      '';
    };
  };

  testScript = ''
    sync_cmd = (
        "fleet-secrets-sync sync --state /var/lib/fleet --host ${hostName}"
        " --repo ${secretsUrl} --signers ${fixtures}/allowed_signers"
    )

    start_all()

    with subtest("1: the forge serves and the fleet host bootstraps"):
        forge.wait_for_unit("sshd.service")
        # A oneshot without RemainAfterExit is inactive once done, which
        # wait_for_unit rejects, so wait for the checkout and read the result.
        fleet.wait_until_succeeds(
            "systemctl show -p ActiveState --value fleet-bootstrap.service | grep -x inactive"
            " && test -e /etc/nixos/.git"
        )
        fleet.succeed("systemctl show -p Result --value fleet-bootstrap.service | grep -x success")

    with subtest("2: the snapshot is in place and holds no repository"):
        fleet.succeed("test -L /var/lib/fleet/secrets")
        fleet.succeed("test -f /var/lib/fleet/secrets/hosts/${hostName}.yaml")
        fleet.succeed("test -f /var/lib/fleet/secrets/users/alice/${hostName}.yaml")
        found = fleet.succeed("find /var/lib/fleet -name .git").strip()
        assert found == "", f"unexpected .git under /var/lib/fleet: {found}"
        fleet.succeed("test -e /etc/nixos/.git")

    with subtest("3: activation deploys the secrets"):
        # The first activation ran before the snapshot existed.
        fleet.succeed("/run/current-system/bin/switch-to-configuration switch")
        fleet.succeed("test -e /run/secrets-for-users/password/alice")
        out = fleet.succeed("stat -c '%U %a' /run/secrets/users/alice/ssh_key").strip()
        assert out == "alice 400", f"ssh_key is '{out}', expected 'alice 400'"

    with subtest("4: alice logs in on the console with her deployed password"):
        fleet.wait_until_tty_matches("1", "login: ")
        fleet.send_chars("alice\n")
        fleet.wait_until_tty_matches("1", "Password")
        fleet.send_chars("alice\n")
        fleet.wait_until_tty_matches("1", "alice@")

    with subtest("5: the maintainer reaches the support account with its key"):
        out = forge.succeed(
            "ssh -i /etc/test-keys/support_ed25519 -o StrictHostKeyChecking=no"
            " -o UserKnownHostsFile=/dev/null support@fleet sudo -n id -u"
        ).strip()
        assert out == "0", f"sudo id -u printed '{out}', expected '0'"

    with subtest("6: a second sync succeeds and names the snapshot's commit"):
        current = fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip()
        status, out = fleet.execute(sync_cmd + " 2>/dev/null")
        assert status == 0, f"second sync exited {status}: {out}"
        assert out.strip() == current, f"sync printed '{out.strip()}', snapshot is '{current}'"

    with subtest("7: an unsigned commit is refused and the snapshot stays"):
        forge.succeed(
            "su git -c '"
            "cd $(mktemp -d) && git clone /var/lib/forge/secrets.git w && cd w"
            " && git -c user.name=x -c user.email=x@test -c commit.gpgsign=false"
            " commit --allow-empty -m unsigned && git push origin main'"
        )
        status, out = fleet.execute(sync_cmd)
        assert status == 11, f"sync after unsigned commit exited {status}, expected 11: {out}"
        after = fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip()
        assert after == current, f"snapshot moved from {current} to {after}"

    # The forge as the maintainer's machine: a signing clone of the secrets
    # repository, the recovery key for sops, and the support key for the
    # host, whose key it knows.
    forge.succeed(
        "install -d -o git -m 700 /home/git/.ssh",
        "install -o git -m 600 /etc/test-keys/recovery.key /etc/test-keys/signing_ed25519 /home/git/",
        "install -o git -m 600 /etc/test-keys/support_ed25519 /home/git/.ssh/id_ed25519",
        "ssh-keyscan -t ed25519 fleet > /home/git/.ssh/known_hosts 2>/dev/null",
        "chown git /home/git/.ssh/known_hosts",
        "su git -c 'git clone -q /var/lib/forge/secrets.git /home/git/w"
        " && git -C /home/git/w config user.name maintainer"
        " && git -C /home/git/w config user.email maintainer@test"
        " && git -C /home/git/w config gpg.format ssh"
        " && git -C /home/git/w config user.signingKey /home/git/signing_ed25519"
        " && git -C /home/git/w config commit.gpgSign true'",
    )

    def maintainer(args, stdin=""):
        # fleet, non-interactive, in the maintainer's clone; sops decrypts with
        # the recovery key from SOPS_AGE_KEY_FILE.
        return forge.succeed(
            f"cd /home/git/w && printf %s '{stdin}' | su git -c"
            f" 'SOPS_AGE_KEY_FILE=/home/git/recovery.key fleet host secret {args} --recovery \"\" --yes' 2>&1"
        )

    def host_value(name):
        # A / in the name nests it: a/b is ["a"]["b"], as sops-nix reads it.
        path = "".join(f'["{part}"]' for part in name.split("/"))
        fleet.succeed(sync_cmd)
        return fleet.succeed(
            "SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract"
            f" '{path}' /var/lib/fleet/secrets/hosts/${hostName}.yaml"
        )

    with subtest("8: fleet host secret encrypts a value for the host, which fetches it"):
        out = maintainer("${hostName} test_secret --value-stdin", stdin="s3cret value")
        assert "s3cret" not in out, f"fleet showed the value:\n{out}"
        assert "✓ committed: ${hostName}: test_secret" in out, out
        got = host_value("test_secret")
        assert got == "s3cret value", f"the host decrypted '{got}'"

    with subtest("9: fleet host secret reads a value off the host, never showing it"):
        fleet.succeed("printf 'kept-on-the-host\\n' > /root/hotspot-pass && chmod 600 /root/hotspot-pass")
        out = maintainer("${hostName} from_host --from-host /root/hotspot-pass --address fleet")
        assert "kept-on-the-host" not in out, f"fleet showed the value:\n{out}"
        assert "ForwardAgent=no" in out and "support@fleet" in out, out
        got = host_value("from_host")
        assert got == "kept-on-the-host", f"the host decrypted '{got}' (the trailing newline should be dropped)"

    with subtest("10: fleet host secret groups secrets under a name with a /"):
        out = maintainer("${hostName} group/first --value-stdin", stdin="first value")
        assert "✓ committed: ${hostName}: group/first" in out, out
        maintainer("${hostName} group/second --value-stdin", stdin="second value")
        assert host_value("group/first") == "first value"
        assert host_value("group/second") == "second value"
        # One value would replace both, so the name of the group is refused.
        status, out = forge.execute(
            "cd /home/git/w && printf x | su git -c"
            " 'SOPS_AGE_KEY_FILE=/home/git/recovery.key fleet host secret ${hostName} group --value-stdin --recovery \"\" --yes' 2>&1"
        )
        assert status != 0 and "group holds secrets of its own (first, second)" in out, out
        assert host_value("group/first") == "first value"
  '';
}
