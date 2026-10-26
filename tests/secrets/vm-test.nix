# End-to-end test of the host side of the secrets design: a `forge` node
# serves signed bare repositories over sshd and a `fleet` node, built from the
# scratch-secrets host, has to bootstrap, snapshot, activate and hold up
# against a tampered repository and an older commit served again, take
# secrets the maintainer adds with `fleet host secret` (the forge also plays
# the maintainer), and fetch its configuration from a private repository with
# its own configuration key (an SSH conf.fleet.repo.url), refusing what is not
# signed, and activate the running system again when only the secrets changed.
# Keys come from the secrets too: alice logs in with a key that is in no
# configuration, support (keysFromSecrets) with its key in its file on the
# host's branch, and ops unlocks sudo with a key in its file; a key in
# alice's ~/.ssh/authorized_keys does not log in.
# A user the host's file lists (dave) has a branch of his own: the host takes
# his file from it when his key signed the tip, a signer it does not know, the
# wrong recipients, a rollback and a malformed password each keep the previous
# file and are named in user-problems, and a sync with nothing new makes no
# new snapshot. Erin is enrolled with fleet secrets user-enroll and retired
# with user-retire, and the host's sync reads what those commands leave: her
# file from her branch, signed by her own key.
# Dave changes his own password, login key and secrets with fleet user, from a
# directory and home of his own with his identity and signing key, signed by
# him, and the host hands him his secret with fleet-user-secret, to him alone.
# No network and no hosting service are involved. Run it with
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
    nativeBuildInputs = with pkgs; [ age openssh sops git mkpasswd coreutils findutils ];
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
    ssh-keygen -q -t ed25519 -N "" -C "fleet-config@${hostName}" -f "$keys/config_ed25519"
    ssh-keygen -q -t ed25519 -N "" -C "maintainer@test" -f "$keys/signing_ed25519"
    ssh-keygen -q -t ed25519 -N "" -C "support@test" -f "$keys/support_ed25519"
    # Keys that are in no configuration, only in the users' files on the
    # host's branch: alice's login key, and ops' sudo key besides one that
    # is only ops' login key.
    for k in alice_login ops_sudo ops_login dave_login; do
      ssh-keygen -q -t ed25519 -N "" -C "$k@test" -f "$keys/$k"
    done
    echo "maintainer@test $(cut -d' ' -f1,2 "$keys/signing_ed25519.pub")" > "$out/allowed_signers"
    cp "$keys/support_ed25519" "$keys/support_ed25519.pub" "$keys/deploy_ed25519.pub" "$keys/config_ed25519.pub" "$out/"
    cp "$keys/alice_login" "$keys/alice_login.pub" "$keys/ops_sudo" "$keys/ops_sudo.pub" "$keys/ops_login" "$out/"
    cp "$keys/dave_login" "$keys/dave_login.pub" "$out/"
    # The maintainer's keys, for the forge to play the maintainer with fleet.
    cp "$keys/recovery.key" "$keys/signing_ed25519" "$out/"
    # Dave, a user with a branch of his own: his signing key and age identity,
    # a key no one trusts, and the host's recipient for his file.
    ssh-keygen -q -t ed25519 -N "" -C "dave@test" -f "$keys/dave_sig"
    ssh-keygen -q -t ed25519 -N "" -C "evil@test" -f "$keys/evil_sig"
    age-keygen -o "$keys/dave.key" 2>/dev/null
    age-keygen -y "$keys/dave.key" > "$out/dave.pub"
    echo "$recipient" > "$out/host.pub"
    cp "$keys/dave_sig" "$keys/dave_sig.pub" "$keys/evil_sig" "$keys/dave.key" "$out/"
    # What dave gives the maintainer, and a file of the keys his commits are
    # checked against (his alone), for the test to see who signed.
    {
      echo "version: 1"
      echo "user: dave"
      echo "age_recipient: $(age-keygen -y "$keys/dave.key")"
      echo "signing_key: $(cut -d' ' -f1,2 "$keys/dave_sig.pub")"
    } > "$out/dave.enroll"
    echo "dave namespaces=\"git\" $(cut -d' ' -f1,2 "$keys/dave_sig.pub")" > "$out/dave_signers"
    # Erin, enrolled with the maintainer's tools: the same keys, and the
    # enrollment file she gives the maintainer.
    ssh-keygen -q -t ed25519 -N "" -C "erin@test" -f "$keys/erin_sig"
    age-keygen -o "$keys/erin.key" 2>/dev/null
    cp "$keys/erin_sig" "$keys/erin.key" "$out/"
    {
      echo "version: 1"
      echo "user: erin"
      echo "age_recipient: $(age-keygen -y "$keys/erin.key")"
      echo "signing_key: $(cut -d' ' -f1,2 "$keys/erin_sig.pub")"
    } > "$out/erin.enroll"

    # The identity document that `os-install --identity` reads, for the host
    # key and both deploy keys.
    {
      echo "version: 1"
      echo "host: ${hostName}"
      echo "age_key: |"
      sed 's/^/  /' "$keys/host.key"
      echo "secrets_deploy_key: |"
      sed 's/^/  /' "$keys/deploy_ed25519"
      echo "config_deploy_key: |"
      sed 's/^/  /' "$keys/config_ed25519"
    } > "$out/identity.yaml"
    # As exported before the keys were renamed and the configuration key
    # existed, for init to read and to add one.
    {
      echo "version: 1"
      echo "host: ${hostName}"
      echo "age_key: |"
      sed 's/^/  /' "$keys/host.key"
      echo "deploy_key: |"
      sed 's/^/  /' "$keys/deploy_ed25519"
    } > "$out/identity-without-config.yaml"

    signed_git() {
      git -c gpg.format=ssh -c "user.signingkey=$keys/signing_ed25519" \
        -c commit.gpgsign=true -c user.name=maintainer -c user.email=maintainer@test "$@"
    }

    # What fleet setup caches for the maintainer's tools.
    echo "$recovery" > "$out/recovery.pub"

    # The secrets repository: main holds a README; the scratch host's branch,
    # host/${hostName}, one signed commit holding both secret files, encrypted
    # to the host and the recovery key.
    git init -q --bare -b main "$out/secrets.git"
    git init -q -b main "$TMPDIR/secrets"
    git -C "$TMPDIR/secrets" remote add origin "$out/secrets.git"
    (
      cd "$TMPDIR/secrets"
      echo "# Scratch secrets" > README.md
      git add -A
      signed_git commit -q -S -m "Scratch secrets repository"
      git push -q origin main
      git switch -q --orphan host/${hostName}
      mkdir -p hosts users/alice users/support users/ops
      printf 'support_password: "%s"\n' "$(mkpasswd -m yescrypt -s <<<support)" > hosts/${hostName}.yaml
      ssh-keygen -q -t ed25519 -N "" -C "alice@${hostName}" -f "$keys/alice_ed25519"
      {
        printf 'password: "%s"\n' "$(mkpasswd -m yescrypt -s <<<alice)"
        echo "ssh_key: |"
        sed 's/^/  /' "$keys/alice_ed25519"
        printf 'ssh_key_pub: "%s"\n' "$(cat "$keys/alice_ed25519.pub")"
        echo "authorized_keys: |"
        sed 's/^/  /' "$keys/alice_login.pub"
      } > users/alice/${hostName}.yaml
      # Support's keys and ops' sudo keys, one per line.
      { echo "authorized_keys: |"; sed 's/^/  /' "$keys/support_ed25519.pub"; } > users/support/${hostName}.yaml
      { echo "sudo_keys: |"; sed 's/^/  /' "$keys/ops_sudo.pub"; } > users/ops/${hostName}.yaml
      sops --encrypt --in-place --age "$recipient,$recovery" hosts/${hostName}.yaml
      for f in users/alice users/support users/ops; do
        sops --encrypt --in-place --age "$recipient,$recovery" $f/${hostName}.yaml
      done
      git add -A
      signed_git commit -q -S -m "Initial scratch secrets"
      git push -q origin host/${hostName}
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
      # Another host's branch, as lib.mkFleet's hostBranches makes them: this
      # host must never fetch it (subtest 18).
      git switch -q --orphan host/other
      git rm -q -rf --ignore-unmatch .
      echo "another host's definition" > other-host.nix
      git add other-host.nix
      signed_git commit -q -S -m "Another host's branch"
      git push -q origin host/other
      git rev-parse HEAD > "$out/other-branch"
    )
  '';

  # Stands in for a configuration that builds, since the VM cannot evaluate a
  # real one (subtest 19; as in tests/fleet/sync-status.nix): a flake with no
  # inputs whose system is a directory with the nixos-version file
  # nixos-rebuild looks for and a switch-to-configuration that records how it
  # was called, can be made to fail, and on switch points /run/current-system
  # at its own directory, so the next sync finds it already running. The
  # records are in /var/lib/stc, not /tmp: the sync has a private /tmp.
  standIn = pkgs.writeText "flake.nix" ''
    {
      outputs = _: {
        nixosConfigurations.${hostName}.config = {
          system.build = {
            toplevel = ./system;
            nixos-rebuild = ./system;
          };
          sops.secrets = { };
        };
      };
    }
  '';
  standInSwitch = pkgs.writeScript "switch-to-configuration" ''
    #!/bin/sh
    echo "$1" >> /var/lib/stc/calls
    if [ "$1" = test ] && [ -e /var/lib/stc/fail ]; then exit 1; fi
    if [ "$1" = switch ]; then
      # With the full paths: systemd-run gives it almost no PATH.
      ${pkgs.coreutils}/bin/ln -sfn \
        "$(${pkgs.coreutils}/bin/dirname "$(${pkgs.coreutils}/bin/dirname "$(${pkgs.coreutils}/bin/readlink -f "$0")")")" \
        /run/current-system
    fi
    exit 0
  '';

  # Runs a command as the given user with an agent that holds one key, as
  # ssh -A would provide it (tests/users/ssh-key-sudo.nix does the same).
  withAgent = pkgs.writeShellScript "with-agent" ''
    user=$1 key=$2; shift 2
    install -m 0600 -o "$user" "${fixtures}/$key" "/tmp/$user-$key"
    exec runuser -u "$user" -- sh -c '
      eval "$(ssh-agent -s)" >/dev/null
      ssh-add -q "$0"
      "$@"
      status=$?
      ssh-agent -k >/dev/null
      exit $status
    ' "/tmp/$user-$key" "$@"
  '';

  # Run on the forge as the git account, which plays the maintainer and, with
  # keys of their own, dave. The maintainer lists dave in the host's file, or
  # puts a file of his on the host's branch.
  listDave = pkgs.writeShellScript "list-dave" ''
    set -eu
    export SOPS_AGE_KEY_FILE=/home/git/recovery.key
    git -C /home/git/w worktree add -q /home/git/hw host/${hostName}
    cd /home/git/hw
    sops set hosts/${hostName}.yaml '["users"]["dave"]' \
      "{\"signing_key\": \"$(cut -d' ' -f1,2 ${fixtures}/dave_sig.pub)\", \"age_recipient\": \"$(cat ${fixtures}/dave.pub)\"}"
    git add -A
    git commit -q -m "list dave"
    git push -q origin host/${hostName}
    cd /
    git -C /home/git/w worktree remove --force /home/git/hw
  '';
  decoyDave = pkgs.writeShellScript "decoy-dave" ''
    set -eu
    git -C /home/git/w worktree add -q /home/git/hw host/${hostName}
    cd /home/git/hw
    mkdir -p users/dave
    printf 'password: "%s"\n' '$y$j9T$salt$hashdecoy' > users/dave/${hostName}.yaml
    sops --encrypt --in-place --age "$(cat ${fixtures}/host.pub),$(cat ${fixtures}/recovery.pub)" users/dave/${hostName}.yaml
    git add -A
    git commit -q -m "a file for dave on the host's branch"
    git push -q origin host/${hostName}
    cd /
    git -C /home/git/w worktree remove --force /home/git/hw
  '';
  # Dave's side: a commit on his branch, signed by SIGNER (dave_sig or
  # evil_sig), his file encrypted to RECIPIENTS (all, or nouser: only the host
  # and the recovery key), with the password named PW (one, two, ...; bad is
  # not a hash).
  daveCommit = pkgs.writeShellScript "dave-commit" ''
    set -eu
    signer=$1 recipients=$2 pw=$3
    if [ ! -d /home/git/d ]; then
      git init -q -b user/dave /home/git/d
      git -C /home/git/d remote add origin /var/lib/forge/secrets.git
    fi
    cd /home/git/d
    mkdir -p users/dave
    case "$pw" in
      bad) hash=plain ;;
      *)   hash='$y$j9T$salt$hash'"$pw" ;;
    esac
    printf 'password: "%s"\n' "$hash" > users/dave/${hostName}.yaml
    age="$(cat ${fixtures}/host.pub),$(cat ${fixtures}/recovery.pub)"
    if [ "$recipients" = all ]; then
      age="$age,$(cat ${fixtures}/dave.pub)"
    fi
    sops --encrypt --in-place --age "$age" users/dave/${hostName}.yaml
    git add -A
    git -c gpg.format=ssh -c "user.signingkey=/home/git/keys/$signer" -c commit.gpgsign=true \
      -c "user.name=$signer" -c "user.email=$signer@test" commit -q -m "$signer: $pw"
    git push -q origin HEAD:refs/heads/user/dave
  '';

  # Dave with fleet, as the user: from a directory and a home of his own, with
  # his age identity (sops reads it from SOPS_AGE_KEY_FILE; the maintainer's
  # recovery key is not in reach), his signing key and his enrollment file, in
  # a clone of the secrets repository of his own.
  daveFleet = pkgs.writeShellScript "dave-fleet" ''
    set -eu
    export HOME=/home/git/dave-home
    export SOPS_AGE_KEY_FILE=/home/git/dave/dave.key
    export GIT_AUTHOR_NAME=dave GIT_AUTHOR_EMAIL=dave@test GIT_COMMITTER_NAME=dave GIT_COMMITTER_EMAIL=dave@test
    cd /home/git/dave
    exec fleet user "$@" --repo /home/git/dave/secrets --user dave --identity "" \
      --signing-key /home/git/dave/dave_sig --enroll /home/git/dave/dave.enroll \
      --signers ${fixtures}/allowed_signers --flake "" --yes
  '';

  # Erin's side: a commit on her branch that sets her password to the hash
  # named PW (erin2), signed by her key, with her age identity for sops.
  erinCommit = pkgs.writeShellScript "erin-commit" ''
    set -eu
    git clone -q -b user/erin /var/lib/forge/secrets.git /home/git/e
    cd /home/git/e
    SOPS_AGE_KEY_FILE=/home/git/keys/erin.key sops set users/erin/${hostName}.yaml '["password"]' "\"\$y\$j9T\$salt\$hash$1\""
    git add -A
    git -c gpg.format=ssh -c user.signingkey=/home/git/keys/erin_sig -c commit.gpgsign=true \
      -c user.name=erin -c user.email=erin@test commit -q -m "erin: password"
    git push -q origin HEAD:refs/heads/user/erin
  '';

  secretsUrl = "ssh://git@forge/var/lib/forge/secrets.git";
  # A private configuration repository, as a fleet's inventory is: its own account
  # on the forge, which takes the host's configuration key and no other.
  configUrl = "ssh://config@forge/var/lib/forge/config.git";
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
    users.users.config = {
      isNormalUser = true;
      openssh.authorizedKeys.keys = [ (builtins.readFile "${fixtures}/config_ed25519.pub") ];
    };

    # sshd runs git-upload-pack through the user's login PATH; fleet is the
    # maintainer's tool.
    environment.systemPackages = [ pkgs.git pkgs.sops pkgs.age pkgs.mkpasswd self.packages.${pkgs.stdenv.hostPlatform.system}.fleet ];

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
        chown -R config:users /var/lib/forge/config.git
      '';
    };

    # For the access check from the forge to the fleet host.
    environment.etc."test-keys/support_ed25519" = {
      source = "${fixtures}/support_ed25519";
      mode = "0600";
    };
    environment.etc."test-keys/alice_login" = {
      source = "${fixtures}/alice_login";
      mode = "0600";
    };
    environment.etc."test-keys/dave_login" = {
      source = "${fixtures}/dave_login";
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

  nodes.fleet = { config, pkgs, nodes, ... }: {
    imports = hostNode.modules ++ [
      {
        conf.hardware.disk.enable = lib.mkForce false;
        disko.devices = lib.mkForce { };
        networking.hostName = lib.mkForce "fleet";
        # For subtest 16: github.com is the forge, as for one in the middle.
        networking.hosts.${nodes.forge.networking.primaryIPAddress} = [ "github.com" ];
        # The test network has a fixed address and no DHCP. NetworkManager,
        # which this host runs, would otherwise try DHCP on it when it sees
        # the interface before the address, give up after a while and take
        # the interface down: a slow boot then lost the forge.
        networking.networkmanager.unmanaged = [ "eth1" ];
        # The test framework turns switching off unless a test asks for it;
        # step 3 runs the activation by hand.
        system.switch.enable = lib.mkForce true;
        # No ESP and no bootable disk in the test VM; the bootloader installer run by the switch
        # would fail on its mount point check.
        boot.loader.systemd-boot.enable = lib.mkForce false;
        boot.loader.grub.enable = lib.mkForce false;
        conf.secrets.repo.url = lib.mkForce secretsUrl;
        conf.fleet.repo.url = lib.mkForce configUrl;
        # Syncs run when a subtest starts one.
        systemd.timers.config-auto-update.enable = false;
        conf.fleet.signing.allowedSignersFile = lib.mkForce "${fixtures}/allowed_signers";
        # Support's key is in its file on the host's branch, and nowhere in
        # the configuration; alice's login key too. Alice is no administrator,
        # so she is let in to sshd by her group; ops is one, whose sudo key is
        # in its file as well.
        conf.fleet.support.sshKeys = lib.mkForce [ ];
        conf.fleet.support.keysFromSecrets = true;
        conf.users.accounts.alice = {
          groups = [ "ssh-users" ];
          secrets.authorizedKeys = true;
        };
        conf.users.accounts.ops = {
          fullName = "Ops";
          privileged = true;
          secrets.password = false;
          secrets.sudoKeys = true;
        };
        # Dave's account, with his login keys and his secrets of his own from
        # his file, which exists only from subtest 21 on and holds those keys
        # only after he adds them (subtest 32): the host switches to this
        # generation then (subtest 33), since sops-nix needs the files of the
        # secrets a generation declares.
        specialisation.dave-own.configuration.conf.users.accounts.dave = {
          fullName = "Dave";
          groups = [ "ssh-users" ];
          secrets.password = false;
          secrets.authorizedKeys = true;
          secrets.own = true;
        };
      }
    ];

    # Root has no ssh configuration for the forge: the secrets fetch uses the
    # host's deploy key and the configuration fetch its configuration key
    # (its repo.url is an SSH address), each as the tools set it, or not at all.
    # sops, to read back what fleet host secret stored, with the host's own key;
    # fleet-clone-flake, the clone os-install makes of a private flake.
    environment.systemPackages = [ pkgs.sops pkgs.openssh (import ../../lib/clone-flake.nix { inherit pkgs; }) ];

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
        " --repo ${secretsUrl} --ref host/${hostName} --signers ${fixtures}/allowed_signers"
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
        # Kernel messages printed after getty's prompt can scroll it off the
        # screen on a slow boot; an empty line makes getty print it again.
        fleet.wait_for_unit("getty@tty1.service")
        fleet.send_key("ret")
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
        # Its key is in no configuration: the account's keys are the file
        # sshd reads, which came from the host's branch.
        fleet.succeed("grep -q support /run/secrets/users/support/authorized_keys")
        fleet.fail("grep -rq support@test /etc/ssh/authorized_keys.d")

    ssh_login = (
        "ssh -i /etc/test-keys/{key} -o IdentitiesOnly=yes -o StrictHostKeyChecking=no"
        " -o UserKnownHostsFile=/dev/null -o BatchMode=yes {user}@fleet {command}"
    )

    with subtest("5b: alice logs in with a key that comes only from her file in the secrets"):
        # Not in the configuration: the rendered sshd configuration has the
        # per-user path, and alice's keys file in /etc has no such key.
        fleet.succeed("grep -E '^AuthorizedKeysFile .*/run/secrets/users/%u/authorized_keys' /etc/ssh/sshd_config")
        fleet.fail("grep -q alice_login /etc/ssh/authorized_keys.d/alice")
        out = forge.succeed(ssh_login.format(key="alice_login", user="alice", command="id -un")).strip()
        assert out == "alice", f"logged in as '{out}'"
        # alice's and 0400: sshd reads it as the user.
        out = fleet.succeed("stat -c '%U %a' /run/secrets/users/alice/authorized_keys").strip()
        assert out == "alice 400", f"authorized_keys is '{out}', expected 'alice 400'"
        # A key in alice's file does not open another account.
        status, _ = forge.execute(ssh_login.format(key="alice_login", user="support", command="true"))
        assert status != 0, "alice's key from the secrets logged in as support"
        # Nor does a key in her home: on a host with secrets sshd reads no
        # ~/.ssh/authorized_keys, which anything running as alice could add to.
        fleet.fail("grep -q '%h/.ssh/authorized_keys' /etc/ssh/sshd_config")
        fleet.succeed(
            "install -d -o alice -m 700 /home/alice/.ssh",
            "install -o alice -m 600 ${fixtures}/support_ed25519.pub /home/alice/.ssh/authorized_keys",
        )
        status, _ = forge.execute(ssh_login.format(key="support_ed25519", user="alice", command="true"))
        assert status != 0, "a key in alice's ~/.ssh/authorized_keys logged in as alice"
        fleet.succeed("rm /home/alice/.ssh/authorized_keys")

    with subtest("5c: sudo by a key from the secrets, for an account that lists it there"):
        assert fleet.succeed("stat -c '%U %a' /run/secrets/users/ops/sudo_keys").strip() == "root 400"
        # No configuration lists a sudo key for ops, so no file in /etc either.
        fleet.fail("test -e /etc/ssh/sudo_keys.d/ops")
        fleet.succeed("${withAgent} ops ops_sudo sudo -n true")
        out = fleet.succeed("${withAgent} ops ops_sudo sudo -n id -u").strip()
        assert out == "0", f"sudo id -u printed '{out}'"
        # Another key, or no agent, does not unlock it, and alice, who has no
        # sudo, gets none from a key in her agent.
        fleet.succeed("runuser -u ops -- sudo -K")
        fleet.fail("${withAgent} ops ops_login sudo -n true")
        fleet.succeed("runuser -u ops -- sudo -K")
        fleet.fail("runuser -u ops -- sudo -n true")
        fleet.fail("${withAgent} alice alice_login sudo -n true")

    with subtest("6: a second sync succeeds and names the snapshot's commit"):
        current = fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip()
        status, out = fleet.execute(sync_cmd + " 2>/dev/null")
        assert status == 0, f"second sync exited {status}: {out}"
        assert out.strip() == current, f"sync printed '{out.strip()}', snapshot is '{current}'"

    with subtest("7: an unsigned commit is refused and the snapshot stays"):
        forge.succeed(
            "su git -c '"
            "cd $(mktemp -d) && git clone -b host/${hostName} /var/lib/forge/secrets.git w && cd w"
            " && git -c user.name=x -c user.email=x@test -c commit.gpgsign=false"
            " commit --allow-empty -m unsigned && git push origin host/${hostName}'"
        )
        status, out = fleet.execute(sync_cmd)
        assert status == 11, f"sync after unsigned commit exited {status}, expected 11: {out}"
        after = fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip()
        assert after == current, f"snapshot moved from {current} to {after}"
        # The forge goes back to the signed commit before it: the maintainer's
        # tools refuse to build on one that no trusted key signed.
        forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git update-ref refs/heads/host/${hostName} refs/heads/host/${hostName}~1'")

    # The forge as the maintainer's machine: a signing clone of the secrets
    # repository, which stays on main while fleet works on the host's branch in
    # a worktree of its own, the recovery key for sops and its recipient as
    # fleet setup caches it, and the support key for the host, whose key it
    # knows.
    forge.succeed(
        "install -d -o git -m 700 /home/git/.ssh",
        "install -o git -m 600 /etc/test-keys/recovery.key /etc/test-keys/signing_ed25519 /home/git/",
        "install -o git -m 600 /etc/test-keys/support_ed25519 /home/git/.ssh/id_ed25519",
        "install -d -o git -m 700 /home/git/keys",
        "install -o git -m 600 ${fixtures}/dave_sig ${fixtures}/evil_sig ${fixtures}/erin_sig ${fixtures}/erin.key /home/git/keys/",
        # Dave's own directory and home for fleet user (daveFleet): his identity,
        # signing key and enrollment file, and nothing of the maintainer's.
        "install -d -o git -m 700 /home/git/dave /home/git/dave-home",
        "install -o git -m 600 ${fixtures}/dave_sig ${fixtures}/dave_sig.pub ${fixtures}/dave.key ${fixtures}/dave.enroll ${fixtures}/dave_signers ${fixtures}/dave_login.pub /home/git/dave/",
        "ssh-keyscan -t ed25519 fleet > /home/git/.ssh/known_hosts 2>/dev/null",
        "chown git /home/git/.ssh/known_hosts",
        "su git -c 'mkdir -p ~/.local/state/fleet && cp ${fixtures}/recovery.pub ~/.local/state/fleet/recovery.pub'",
        "su git -c 'git clone -q /var/lib/forge/secrets.git /home/git/w"
        " && git -C /home/git/w config user.name maintainer"
        " && git -C /home/git/w config user.email maintainer@test"
        " && git -C /home/git/w config gpg.format ssh"
        " && git -C /home/git/w config user.signingKey /home/git/signing_ed25519"
        " && git -C /home/git/w config commit.gpgSign true"
        " && git -C /home/git/w config gpg.ssh.allowedSignersFile ${fixtures}/allowed_signers'",
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

    def user_keys(args):
        # fleet secrets user-keys, in the same clone and with the same keys.
        return forge.execute(
            "cd /home/git/w && su git -c 'SOPS_AGE_KEY_FILE=/home/git/recovery.key"
            f" fleet secrets user-keys {args} --recovery \"\" --yes' 2>&1"
        )

    with subtest("10b: fleet secrets user-keys stores checked keys on the host's branch and pushes"):
        forge.succeed(
            "su git -c 'ssh-keygen -q -t ed25519 -N \"\" -C alice-second@test -f /home/git/alice_second'",
            "su git -c 'cat /home/git/alice_second.pub ${fixtures}/alice_login.pub > /home/git/alice.keys'",
            "su git -c 'printf \"# a comment\\nssh-ed25519 AAAA-typo\\n\" > /home/git/typo.keys'",
        )
        # A line that is not a key stops it before anything is written.
        before = forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/${hostName}'")
        status, out = user_keys("alice ${hostName} --authorized-keys /home/git/typo.keys")
        assert status != 0 and "line 2 is not an SSH public key" in out, f"{status}: {out}"
        assert forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/${hostName}'") == before
        status, out = user_keys("alice ${hostName} --authorized-keys /home/git/alice.keys")
        assert status == 0 and "committed: user-keys: alice on ${hostName}" in out, f"{status}: {out}"
        assert forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/${hostName}'") != before
        # The host takes it from the branch as a snapshot, and reads back both keys.
        fleet.succeed(sync_cmd)
        got = fleet.succeed(
            "SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '[\"authorized_keys\"]'"
            " /var/lib/fleet/secrets/users/alice/${hostName}.yaml"
        )
        want = forge.succeed("cat /home/git/alice.keys")
        assert got.strip() == want.strip(), f"the host decrypted '{got}', expected '{want}'"
        # A file made for a user that had none is encrypted to the host as well.
        status, out = user_keys("carol ${hostName} --sudo-keys ${fixtures}/ops_sudo.pub")
        assert status == 0, out
        fleet.succeed(sync_cmd)
        fleet.succeed(
            "SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '[\"sudo_keys\"]'"
            " /var/lib/fleet/secrets/users/carol/${hostName}.yaml | grep -q ops_sudo@test"
        )

    with subtest("10c: user-keys adds and removes single keys in what is stored, by key material"):
        def tip():
            return forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/${hostName}'")

        def stored(user, key="authorized_keys"):
            # What the host's snapshot holds, decrypted with the host's key.
            fleet.succeed(sync_cmd)
            return fleet.succeed(
                f"SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '[\"{key}\"]'"
                f" /var/lib/fleet/secrets/users/{user}/${hostName}.yaml"
            ).strip().split("\n")

        def key_of(path):
            # The key type and material of a public key file, which is what matches.
            return forge.succeed(f"cut -d' ' -f1,2 {path}").strip()

        forge.succeed(
            "su git -c 'ssh-keygen -q -t ed25519 -N \"\" -C alice-third@test -f /home/git/alice_third'",
            "su git -c 'cat ${fixtures}/alice_login.pub /home/git/alice_third.pub > /home/git/alice.rest'",
        )
        first, login, third = key_of("/home/git/alice_second.pub"), key_of("${fixtures}/alice_login.pub"), key_of("/home/git/alice_third.pub")
        # The first stored key under another comment, and with an option.
        forge.succeed(f"printf '%s\\n' 'from=\"10.0.0.0/8\" {first} renamed' > /home/git/alice_first.renamed")

        # (a) adding a key: a new commit, the keys there stay, in order.
        before = tip()
        status, out = user_keys("alice ${hostName} --add-authorized-keys /home/git/alice_third.pub")
        assert status == 0, out
        assert tip() != before
        keys = stored("alice")
        assert [k.rsplit(" ", 1)[0] for k in keys] == [first, login, third], keys
        assert keys[0].endswith("alice-second@test"), keys

        # (b) the same again changes nothing: no commit.
        before = tip()
        status, out = user_keys("alice ${hostName} --add-authorized-keys /home/git/alice_third.pub")
        assert status == 0 and "nothing to add or remove" in out, f"{status}: {out}"
        assert tip() == before, "a commit was made though nothing changed"

        # (c) removing by material, though the file says another comment and an option.
        status, out = user_keys("alice ${hostName} --remove-authorized-keys /home/git/alice_first.renamed")
        assert status == 0, out
        keys = stored("alice")
        assert [k.rsplit(" ", 1)[0] for k in keys] == [login, third], keys

        # (d) removing every key that is left would lock the account out: refused.
        before = tip()
        status, out = user_keys("alice ${hostName} --remove-authorized-keys /home/git/alice.rest")
        assert status != 0 and "lock the account out" in out, f"{status}: {out}"
        assert tip() == before
        assert len(stored("alice")) == 2

        # (e) a file that has no authorized_keys yet (carol's holds sudo_keys only).
        status, out = user_keys("carol ${hostName} --add-authorized-keys /home/git/alice_third.pub")
        assert status == 0, out
        assert [k.rsplit(" ", 1)[0] for k in stored("carol")] == [third]
        assert any("ops_sudo@test" in k for k in stored("carol", "sudo_keys"))

    with subtest("11: an older signed commit served again is refused, unless the record goes"):
        fleet.succeed(sync_cmd)
        current = fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip()
        accepted = fleet.succeed("cat /var/lib/fleet/accepted/host").strip()
        manifest_host = "sed -n 's/^host //p' /var/lib/fleet/secrets/manifest"
        assert fleet.succeed(manifest_host).strip() == accepted, f"accepted {accepted}, but the snapshot is made from another commit"
        # Someone who can push, with only old signed commits, rewinds the branch.
        older = forge.succeed("su git -c 'git -C /home/git/w rev-parse host/${hostName}~1'").strip()
        forge.succeed(f"su git -c 'git -C /home/git/w push -q --force origin {older}:host/${hostName}'")
        status, out = fleet.execute(sync_cmd)
        assert status == 16, f"sync after a rollback exited {status}, expected 16: {out}"
        assert fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip() == current
        assert fleet.succeed("cat /var/lib/fleet/accepted/host").strip() == accepted
        # Removing the record accepts it, for a history rewritten on purpose.
        fleet.succeed("rm /var/lib/fleet/accepted/host")
        out = fleet.succeed(sync_cmd + " 2>/dev/null").strip()
        assert out == fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip(), f"sync printed {out}"
        assert fleet.succeed(manifest_host).strip() == older
        assert fleet.succeed("cat /var/lib/fleet/accepted/host").strip() == older

    def unsigned_config_commit():
        # As anyone with write access to the repository, without the signing key.
        forge.succeed(
            "su config -c '"
            "cd $(mktemp -d) && git clone -q /var/lib/forge/config.git w && cd w"
            " && git -c user.name=x -c user.email=x@test -c commit.gpgsign=false"
            " commit -q --allow-empty -m unsigned && git push -q origin main'"
        )
        return forge.succeed("su config -c 'git -C /var/lib/forge/config.git rev-parse main'").strip()

    def recorded():
        return fleet.succeed("cat /var/lib/fleet-sync/status")

    with subtest("12: the configuration sync fetches with the host's own key, and verifies"):
        unsigned = unsigned_config_commit()
        fleet.execute("systemctl start config-auto-update.service")
        # Only the configuration key opens the config account, so the commit
        # can only have been fetched with it; then it is refused.
        status = recorded()
        assert "kind=signature" in status and f"commit={unsigned}" in status, status
        fleet.succeed("test \"$(git -C /etc/nixos rev-parse HEAD)\" != " + unsigned)

    with subtest("13: the sync fetches from the repository its configuration names"):
        # As on a host whose configuration has just moved to another repository.
        fleet.succeed("git -C /etc/nixos remote set-url origin ssh://git@forge/var/lib/forge/old.git")
        unsigned = unsigned_config_commit()
        fleet.execute("systemctl start config-auto-update.service")
        url = fleet.succeed("git -C /etc/nixos remote get-url origin").strip()
        assert url == "${configUrl}", f"origin is {url}"
        assert f"commit={unsigned}" in recorded(), recorded()

    with subtest("14: init reads an older identity, and adds a configuration key to it"):
        # The document says deploy_key and has no configuration key.
        fleet.succeed(
            "fleet-secrets-sync init --state /tmp/old --host ${hostName}"
            " --from-identity ${fixtures}/identity-without-config.yaml"
        )
        secrets_pub = fleet.succeed("cat /tmp/old/secrets_deploy_key.pub").strip()
        pub = fleet.succeed("cat /tmp/old/config_deploy_key.pub").strip()
        fleet.succeed("test \"$(stat -c %a /tmp/old/config_deploy_key)\" = 600")
        enroll = fleet.succeed("cat /tmp/old/${hostName}.enroll")
        assert f"secrets_deploy_pubkey: {secrets_pub}" in enroll, enroll
        assert f"config_deploy_pubkey: {pub}" in enroll, enroll
        before = fleet.succeed("sha256sum /tmp/old/* | sort")
        fleet.succeed("fleet-secrets-sync init --state /tmp/old --host ${hostName}")
        assert fleet.succeed("sha256sum /tmp/old/* | sort") == before, "a second init changed the identity"
        # The host's own enrollment file lists the key the config account takes.
        real = fleet.succeed("cat /var/lib/fleet/${hostName}.enroll")
        assert "config_deploy_pubkey: " + fleet.succeed("cat /var/lib/fleet/config_deploy_key.pub").strip() in real, real

    with subtest("15: a host from before the keys were renamed keeps its deploy key"):
        # Its state as it was: deploy_ed25519, and an enrollment file that says deploy_pubkey.
        fleet.succeed(
            "install -d -m 700 /tmp/legacy",
            "cp -a /tmp/old/age.key /tmp/legacy/",
            "cp -a /tmp/old/secrets_deploy_key /tmp/legacy/deploy_ed25519",
            "cp -a /tmp/old/secrets_deploy_key.pub /tmp/legacy/deploy_ed25519.pub",
        )
        fleet.succeed("fleet-secrets-sync init --state /tmp/legacy --host ${hostName}")
        fleet.succeed("test -f /tmp/legacy/secrets_deploy_key -a ! -e /tmp/legacy/deploy_ed25519")
        assert fleet.succeed("cat /tmp/legacy/secrets_deploy_key.pub").strip() == secrets_pub
        enroll = fleet.succeed("cat /tmp/legacy/${hostName}.enroll")
        assert f"secrets_deploy_pubkey: {secrets_pub}" in enroll and "\ndeploy_pubkey" not in enroll, enroll
        # The sync, too, renames a key it finds under the old name.
        fleet.succeed(
            "mv /var/lib/fleet/secrets_deploy_key /var/lib/fleet/deploy_ed25519",
            "mv /var/lib/fleet/secrets_deploy_key.pub /var/lib/fleet/deploy_ed25519.pub",
        )
        fleet.succeed(sync_cmd)
        fleet.succeed("test -f /var/lib/fleet/secrets_deploy_key -a ! -e /var/lib/fleet/deploy_ed25519")

    with subtest("15b: a host whose keys were named after their type keeps them"):
        fleet.succeed(
            "install -d -m 700 /tmp/legacy2",
            "cp -a /tmp/old/age.key /tmp/legacy2/",
            "cp -a /tmp/old/secrets_deploy_key /tmp/legacy2/secrets_deploy_ed25519",
            "cp -a /tmp/old/secrets_deploy_key.pub /tmp/legacy2/secrets_deploy_ed25519.pub",
            "cp -a /tmp/old/config_deploy_key /tmp/legacy2/config_deploy_ed25519",
            "cp -a /tmp/old/config_deploy_key.pub /tmp/legacy2/config_deploy_ed25519.pub",
        )
        fleet.succeed("fleet-secrets-sync init --state /tmp/legacy2 --host ${hostName}")
        for name in ["secrets_deploy_key", "config_deploy_key"]:
            fleet.succeed(f"test \"$(stat -c %a /tmp/legacy2/{name})\" = 600")
        fleet.succeed("test ! -e /tmp/legacy2/secrets_deploy_ed25519 -a ! -e /tmp/legacy2/secrets_deploy_ed25519.pub")
        fleet.succeed("test ! -e /tmp/legacy2/config_deploy_ed25519 -a ! -e /tmp/legacy2/config_deploy_ed25519.pub")
        assert fleet.succeed("cat /tmp/legacy2/secrets_deploy_key.pub").strip() == secrets_pub
        assert fleet.succeed("cat /tmp/legacy2/config_deploy_key.pub").strip() == pub

    with subtest("15c: check finds nested keys and whole-file secrets"):
        # check never decrypts, so the snapshot here is plain YAML; the flake has
        # no inputs (the VM has no network) and declares only the secrets.
        fleet.succeed(
            "install -d -m 700 /tmp/check-state /tmp/check-state/secrets /tmp/check-state/secrets/hosts",
            "install -d /tmp/check-flake",
            "printf 'flat: ENC[x]\nhotspot:\n  wls0: ENC[y]\nsops:\n  mac: ENC[z]\n' > /tmp/check-state/secrets/hosts/h.yaml",
            "cp ${pkgs.writeText "check-flake.nix" ''
              {
                outputs = _:
                  let
                    s = key: { sopsFile = "/x/secrets/hosts/h.yaml"; inherit key; };
                    host = secrets: { config.sops.secrets = secrets; };
                    good = { flat = s "flat"; "hotspot/wls0" = s "hotspot/wls0"; whole = s ""; };
                  in {
                    nixosConfigurations.good = host good;
                    nixosConfigurations.bad = host (good // { nope = s "hotspot/nope"; });
                  };
              }
            ''} /tmp/check-flake/flake.nix",
        )
        check_cmd = (
            "NIX_CONFIG='extra-experimental-features = nix-command flakes'"
            " fleet-secrets-sync check --state /tmp/check-state --config path:/tmp/check-flake --host "
        )
        fleet.succeed(check_cmd + "good")
        status, out = fleet.execute(check_cmd + "bad 2>&1")
        assert status == 12, f"check exited {status}: {out}"
        assert "missing key hotspot/nope" in out, out

    with subtest("15d: check refuses keys from a file that lacks them"):
        # A configuration whose account takes its login keys from the secrets
        # (the secret users/<name>/authorized_keys) while the account's file
        # on the branch holds a password only; support's file has its keys.
        fleet.succeed(
            "install -d -m 700 /tmp/check-state/secrets/users /tmp/check-state/secrets/users/carol /tmp/check-state/secrets/users/support",
            "install -d /tmp/check-keys-flake",
            "printf 'password: ENC[x]\nsops:\n  mac: ENC[z]\n' > /tmp/check-state/secrets/users/carol/h.yaml",
            "printf 'authorized_keys: ENC[x]\nsops:\n  mac: ENC[z]\n' > /tmp/check-state/secrets/users/support/h.yaml",
            "cp ${pkgs.writeText "check-keys-flake.nix" ''
              {
                outputs = _:
                  let
                    keys = user: { sopsFile = "/x/secrets/users/''${user}/h.yaml"; key = "authorized_keys"; };
                    host = secrets: { config.sops.secrets = secrets; };
                  in {
                    nixosConfigurations.keys = host { "users/support/authorized_keys" = keys "support"; };
                    nixosConfigurations.nokeys = host {
                      "users/support/authorized_keys" = keys "support";
                      "users/carol/authorized_keys" = keys "carol";
                    };
                  };
              }
            ''} /tmp/check-keys-flake/flake.nix",
        )
        keys_check = (
            "NIX_CONFIG='extra-experimental-features = nix-command flakes'"
            " fleet-secrets-sync check --state /tmp/check-state --config path:/tmp/check-keys-flake --host "
        )
        fleet.succeed(keys_check + "keys")
        status, out = fleet.execute(keys_check + "nokeys 2>&1")
        assert status == 12, f"check exited {status}: {out}"
        assert "missing key authorized_keys in" in out and "users/carol/h.yaml" in out, out

    with subtest("16: a forge answering for github.com without GitHub's host key is refused"):
        # github.com resolves to the forge here, as for a machine in the
        # middle: it serves the same repository, with its own host key.
        before = fleet.succeed("readlink /var/lib/fleet/secrets").strip()
        status, out = fleet.execute(
            sync_cmd.replace("${secretsUrl}", "ssh://git@github.com/var/lib/forge/secrets.git") + " 2>&1"
        )
        assert status == 10, f"sync through a forge posing as github.com exited {status}: {out}"
        assert "did not show the host key this host knows" in out, out
        assert fleet.succeed("readlink /var/lib/fleet/secrets").strip() == before
        fleet.fail("grep -q github.com /var/lib/fleet/known_hosts")

    with subtest("17: an install's clone of a private flake takes exactly the commit asked for"):
        # As os-install clones it, with the host's configuration key. The
        # commit before the newest stands for one checked on the maintainer's
        # machine, with a newer one pushed since.
        older = forge.succeed("su config -c 'git -C /var/lib/forge/config.git rev-parse main~1'").strip()
        ssh = (
            "ssh -i /var/lib/fleet/config_deploy_key -o IdentitiesOnly=yes"
            " -o UserKnownHostsFile=/var/lib/fleet/known_hosts -o StrictHostKeyChecking=accept-new"
        )
        clone = f"GIT_SSH_COMMAND='{ssh}' fleet-clone-flake 'git+${configUrl}?ref=main&rev="
        fleet.succeed(f"{clone}{older}' /tmp/pinned")
        assert fleet.succeed("git -C /tmp/pinned rev-parse HEAD").strip() == older
        status, out = fleet.execute(f"{clone}{'0' * 40}' /tmp/missing 2>&1")
        assert status == 3 and "is it pushed?" in out, f"exit {status}: {out}"
        fleet.fail("test -e /tmp/missing")

    with subtest("18: the host fetches only the branch it tracks"):
        # The forge also has another host's branch, as with lib.mkFleet's
        # hostBranches. Neither the first-boot clone nor any sync since
        # fetched it, and a git fetch by hand would not either.
        other = fleet.succeed("cat ${fixtures}/other-branch").strip()
        fleet.fail(f"git -C /etc/nixos cat-file -e {other}")
        refs = fleet.succeed("git -C /etc/nixos for-each-ref --format='%(refname)'")
        assert "other" not in refs, refs
        fetch = fleet.succeed("git -C /etc/nixos config --get-all remote.origin.fetch").strip()
        assert fetch == "+refs/heads/main:refs/remotes/origin/main", fetch
        # Nor does the clone os-install makes.
        fleet.succeed(f"GIT_SSH_COMMAND='{ssh}' fleet-clone-flake 'git+${configUrl}?ref=main' /tmp/one-branch")
        fleet.fail(f"git -C /tmp/one-branch cat-file -e {other}")

    with subtest("19: a change to the secrets alone reaches the running system"):
        # The stand-in configuration goes onto the configuration branch as a
        # signed commit on top of its tip (the unsigned commits of 12 and 13
        # are below it, and only the tip is verified). The maintainer's key is
        # given to the config account for it, as the git account has it for
        # the secrets. The stand-in system links to the tools of the system
        # running now as its sw: the test driver finds its shell through
        # /run/current-system/sw once the stand-in is the running system.
        sw = fleet.succeed("readlink -f /run/current-system/sw").strip()
        forge.succeed(
            "install -o config -m 600 /etc/test-keys/signing_ed25519 /home/config/",
            "su config -c 'cd $(mktemp -d) && git clone -q /var/lib/forge/config.git w && cd w"
            " && git rm -q -r -f . && cp ${standIn} flake.nix"
            " && install -D -m 755 ${standInSwitch} system/bin/switch-to-configuration"
            f" && echo stand-in > system/nixos-version && ln -s {sw} system/sw && git add -A"
            " && git -c user.name=maintainer -c user.email=maintainer@test -c gpg.format=ssh"
            " -c user.signingKey=/home/config/signing_ed25519 -c commit.gpgSign=true"
            " commit -q -m stand-in && git push -q origin main'"
        )
        fleet.succeed("install -d /var/lib/stc")

        def calls():
            return fleet.succeed("cat /var/lib/stc/calls").split()

        def applied():
            return fleet.succeed("cat /var/lib/fleet-sync/secrets-applied").strip()

        def link():
            return fleet.succeed("readlink /var/lib/fleet/secrets").strip()

        def run_sync():
            # Whether the run succeeded, and the journal of this run alone.
            before = fleet.succeed("journalctl -u config-auto-update")
            status, _ = fleet.execute("systemctl start config-auto-update.service")
            return status, fleet.succeed("journalctl -u config-auto-update")[len(before):]

        def push_secrets_commit():
            # A signed commit on the host's branch, on top of the local branch,
            # which still holds the commit the host accepted last (11 only
            # rewound the forge's): the clone itself stays on main.
            forge.succeed(
                "su git -c 'cd /home/git/w"
                " && c=$(git commit-tree -S -p host/${hostName} -m secrets-only host/${hostName}^{tree})"
                " && git update-ref refs/heads/host/${hostName} $c"
                " && git push -q origin host/${hostName}'"
            )

        status, out = run_sync()
        assert status == 0, out
        assert calls() == ["switch"], calls()
        fleet.succeed("grep -qx stand-in /run/current-system/nixos-version")
        fleet.fail("test -e /var/lib/fleet-sync/status")
        assert applied() == link(), (applied(), link())

        first = link()
        push_secrets_commit()
        status, out = run_sync()
        assert status == 0, out
        second = link()
        assert second != first, "the snapshot did not move"
        assert calls() == ["switch", "test"], calls()
        assert "activating the running system again" in out, out
        assert applied() == second, (applied(), second)
        fleet.fail("test -e /var/lib/fleet-sync/status")

        # Nothing new: the same system, the same secrets, nothing to do.
        status, out = run_sync()
        assert status == 0, out
        assert calls() == ["switch", "test"], calls()
        assert "already running" in out, out

        # An activation that fails stops the sync and leaves the record, so the
        # next run, with nothing new, tries again.
        fleet.succeed("touch /var/lib/stc/fail")
        push_secrets_commit()
        status, out = run_sync()
        assert status != 0, out
        assert calls() == ["switch", "test", "test"], calls()
        status_file = fleet.succeed("cat /var/lib/fleet-sync/status")
        assert "kind=activation" in status_file, status_file
        assert applied() == second, (applied(), second)
        fleet.succeed("rm /var/lib/stc/fail")
        status, out = run_sync()
        assert status == 0, out
        assert calls() == ["switch", "test", "test", "test"], calls()
        assert applied() == link() != second, (applied(), link())
        fleet.fail("test -e /var/lib/fleet-sync/status")

    # Dave has a branch of his own. As the forge: the maintainer lists him in
    # the host's file, and he, with his own keys, commits to user/dave.
    def as_git(command):
        return forge.succeed(f"su git -c '{command}'")

    def dave(signer, recipients, pw):
        as_git(f"${daveCommit} {signer} {recipients} {pw}")
        return as_git("git -C /home/git/d rev-parse HEAD").strip()

    def force_dave(rev):
        as_git(f"git -C /home/git/d push -q --force origin {rev}:refs/heads/user/dave")

    def sync_ok():
        status, out = fleet.execute(sync_cmd + " 2>/dev/null")
        assert status == 0, f"sync exited {status}: {out}"
        return out.strip()

    def manifest():
        return fleet.succeed("cat /var/lib/fleet/secrets/manifest")

    def user_problems():
        return fleet.succeed("cat /var/lib/fleet/user-problems")

    def dave_password():
        return fleet.succeed(
            "SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '[\"password\"]'"
            " /var/lib/fleet/secrets/users/dave/${hostName}.yaml"
        )

    def password_hash(pw):
        return "$y$j9T$salt$hash" + pw

    def accepted_dave():
        return fleet.succeed("cat /var/lib/fleet/accepted/user-dave").strip()

    with subtest("20: a user the host's file lists, with no branch yet, is named and the host carries on"):
        first = link()
        as_git("${listDave}")
        sync_ok()
        assert link() != first, "listing dave left the snapshot as it was"
        assert "dave has no branch user/dave" in user_problems(), user_problems()
        fleet.succeed("test \"$(stat -c %a /var/lib/fleet/user-problems)\" = 644")
        fleet.fail("test -e /var/lib/fleet/secrets/users/dave")
        # The host's own file decrypts, and alice still comes from the host's branch.
        assert host_value("test_secret") == "s3cret value"
        assert "user alice host-branch" in manifest(), manifest()
        fleet.succeed("test -f /var/lib/fleet/secrets/users/alice/${hostName}.yaml")

    with subtest("21: with his branch the sync takes dave's file from it, and the manifest says so"):
        tip = dave("dave_sig", "all", "one")
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert f"user dave {tip}\n" in manifest(), manifest()
        assert "user alice host-branch\n" in manifest(), manifest()
        assert "host-branch" not in [l for l in manifest().split("\n") if l.startswith("user dave")][0]
        assert dave_password() == password_hash("one"), dave_password()
        assert accepted_dave() == tip
        # The snapshot is named by the hash of its manifest.
        name = fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip()
        assert name == fleet.succeed("sha256sum < /var/lib/fleet/secrets/manifest | cut -d' ' -f1").strip(), name

    with subtest("22: a file for a listed user on the host's branch is ignored"):
        as_git("${decoyDave}")
        sync_ok()
        assert dave_password() == password_hash("one"), dave_password()
        assert "user dave host-branch" not in manifest(), manifest()
        assert f"user dave {tip}\n" in manifest(), manifest()

    with subtest("23: a commit signed by dave reaches the snapshot"):
        before = link()
        tip = dave("dave_sig", "all", "two")
        sync_ok()
        assert link() != before
        assert f"user dave {tip}\n" in manifest(), manifest()
        assert dave_password() == password_hash("two"), dave_password()
        assert accepted_dave() == tip

    with subtest("24: a sync with nothing new makes no new snapshot"):
        before = link()
        out = sync_ok()
        assert link() == before, (before, link())
        assert out == fleet.succeed("basename $(readlink /var/lib/fleet/secrets)").strip(), out
        assert fleet.succeed("ls /var/lib/fleet/snapshots").split() == [out], out
        fleet.fail("test -e /var/lib/fleet/user-problems")

    with subtest("25: a commit signed by an unknown key keeps the file, and the host and alice go on"):
        good = tip
        dave("evil_sig", "all", "three")
        out = maintainer("${hostName} second_secret --value-stdin", stdin="another value")
        assert "✓ committed" in out, out
        assert host_value("second_secret") == "another value"
        assert dave_password() == password_hash("two"), dave_password()
        problem = user_problems()
        assert problem.startswith("dave ") and "not signed" in problem, problem
        assert f"user dave kept {good}\n" in manifest(), manifest()
        assert "user alice host-branch\n" in manifest(), manifest()
        assert accepted_dave() == good

    with subtest("26: the notice every account gets names dave, and goes when the problem does"):
        status, out = run_sync()
        assert status == 0, out
        fleet.succeed("test \"$(stat -c %a /var/lib/fleet-sync/user-problems)\" = 644")
        out = fleet.succeed("runuser -u alice -- fleet-sync-status")
        assert "dave:" in out and "not signed" in out, out
        out = fleet.succeed("su -l alice -c true 2>&1")
        assert "Some users' secrets on this machine have not updated" in out, out
        fleet.fail("test -e /var/lib/fleet-sync/status")

    with subtest("27: dave's next good commit, on top of the bad one, restores his file"):
        tip = dave("dave_sig", "all", "three")
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert dave_password() == password_hash("three"), dave_password()
        assert f"user dave {tip}\n" in manifest(), manifest()
        status, out = run_sync()
        assert status == 0, out
        fleet.fail("test -e /var/lib/fleet-sync/user-problems")
        assert "working" in fleet.succeed("runuser -u alice -- fleet-sync-status")

    with subtest("28: a file not encrypted to dave, this host and the recovery key is kept out"):
        good = tip
        dave("dave_sig", "nouser", "four")
        sync_ok()
        assert dave_password() == password_hash("three"), dave_password()
        assert "not encrypted to exactly" in user_problems(), user_problems()
        assert f"user dave kept {good}\n" in manifest(), manifest()
        tip = dave("dave_sig", "all", "four")
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert dave_password() == password_hash("four"), dave_password()

    with subtest("29: a password that is not a hash is kept out"):
        good = tip
        dave("dave_sig", "all", "bad")
        sync_ok()
        assert dave_password() == password_hash("four"), dave_password()
        assert "not a password hash" in user_problems(), user_problems()
        assert f"user dave kept {good}\n" in manifest(), manifest()
        tip = dave("dave_sig", "all", "five")
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert dave_password() == password_hash("five"), dave_password()

    with subtest("30: dave's branch forced back to an older commit is a rollback; restoring it clears the problem"):
        older = as_git("git -C /home/git/d rev-parse HEAD~1").strip()
        force_dave(older)
        sync_ok()
        assert dave_password() == password_hash("five"), dave_password()
        assert "does not follow" in user_problems(), user_problems()
        assert f"user dave kept {tip}\n" in manifest(), manifest()
        assert accepted_dave() == tip
        assert host_value("second_secret") == "another value"
        force_dave(tip)
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert f"user dave {tip}\n" in manifest(), manifest()
        assert dave_password() == password_hash("five"), dave_password()

    with subtest("31: a user enrolled and retired with fleet secrets is read by the host's sync as the tools leave her"):
        def fleet_cmd(args, stdin=""):
            # fleet secrets ..., in the maintainer's clone, with the recovery key.
            return forge.succeed(
                f"cd /home/git/w && printf %s '{stdin}' | su git -c"
                f" 'SOPS_AGE_KEY_FILE=/home/git/recovery.key fleet secrets {args} --recovery \"\" --yes' 2>&1"
            )

        def erin_file(key):
            return fleet.succeed(
                f"SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '[\"{key}\"]'"
                " /var/lib/fleet/secrets/users/erin/${hostName}.yaml"
            )

        def listed():
            return fleet.succeed(
                "SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '[\"users\"]'"
                " /var/lib/fleet/secrets/hosts/${hostName}.yaml"
            )

        # Her password first, on the host's branch, as a maintainer sets it for a
        # user who has no branch yet; enrolling moves the file to her branch.
        fleet_cmd("user-password erin ${hostName} --password-stdin", stdin="erin-secret")
        out = fleet_cmd("user-enroll erin --from ${fixtures}/erin.enroll --host ${hostName}")
        assert "erin-secret" not in out, out
        erin_tip = as_git("git -C /var/lib/forge/secrets.git rev-parse user/erin").strip()
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert f"user erin {erin_tip}\n" in manifest(), manifest()
        # The hash is her password: the same salt, hashed again, gives it.
        hashed = erin_file("password").strip()
        salt = "$".join(hashed.split("$")[2:4])
        forge.succeed(f"test \"$(printf %s erin-secret | mkpasswd -m yescrypt -S '{salt}' -s)\" = '{hashed}'")
        users = listed()
        assert "erin:" in users and "ssh-ed25519" in users, users
        assert "user alice host-branch\n" in manifest(), manifest()

        # Her own commit, signed by her key and not the maintainer's.
        as_git("${erinCommit} erin2")
        erin_tip = as_git("git -C /home/git/e rev-parse HEAD").strip()
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert f"user erin {erin_tip}\n" in manifest(), manifest()
        assert erin_file("password").strip() == password_hash("erin2"), erin_file("password")

        # Retired: the host stops reading her branch, which the forge still has.
        fleet_cmd("user-retire erin")
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert "erin" not in listed(), listed()
        assert "user erin" not in manifest(), manifest()
        fleet.fail("test -e /var/lib/fleet/secrets/users/erin")

    # Dave as the user, with fleet user, from his own directory and home: his
    # identity and signing key, in a clone of his own (daveFleet).
    forge.succeed("su git -c 'git clone -q /var/lib/forge/secrets.git /home/git/dave/secrets'")

    def dave_fleet(args, stdin=""):
        return forge.succeed(f"printf %s '{stdin}' | su git -c '${daveFleet} {args}' 2>&1")

    def dave_value(*path):
        key = "".join(f'["{part}"]' for part in path)
        return fleet.succeed(
            f"SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d --extract '{key}'"
            " /var/lib/fleet/secrets/users/dave/${hostName}.yaml"
        )

    def dave_tip():
        return as_git("git -C /var/lib/forge/secrets.git rev-parse user/dave").strip()

    with subtest("32: dave changes his password, keys and secrets with fleet user, signed by his own key"):
        before = dave_tip()
        out = dave_fleet("password ${hostName} --password-stdin", stdin="dave-secret")
        assert "dave-secret" not in out and "committed: user-password: dave on ${hostName}" in out, out
        # His commit, on top of the last, signed by his key and by no other.
        pushed = dave_tip()
        assert pushed != before
        assert as_git("git -C /var/lib/forge/secrets.git rev-parse user/dave~1").strip() == before
        assert as_git(
            "git -C /var/lib/forge/secrets.git -c gpg.ssh.allowedSignersFile=${fixtures}/dave_signers"
            " log -1 --format=%G? user/dave"
        ).strip() == "G"
        assert as_git("git -C /var/lib/forge/secrets.git log -1 --format=%an user/dave").strip() == "dave"

        out = dave_fleet("secret set token ${hostName} --value-stdin", stdin="tok-123")
        assert "tok-123" not in out and "committed: user-secret: set token for dave on ${hostName}" in out, out
        # The same value again changes nothing: no commit, no push.
        pushed = dave_tip()
        out = dave_fleet("secret set token ${hostName} --value-stdin", stdin="tok-123")
        assert "nothing changed" in out and dave_tip() == pushed, out
        dave_fleet("secret set junk --all-hosts --value-stdin", stdin="x")
        out = dave_fleet("secret unset junk ${hostName}")
        assert "committed: user-secret: unset junk for dave on ${hostName}" in out, out
        out = dave_fleet("secret unset junk --all-hosts")
        assert "nothing changed" in out, out

        # A login key: replaces nothing but adds to the file.
        out = dave_fleet("keys ${hostName} --authorized-keys /home/git/dave/dave_login.pub")
        assert "committed: user-keys: dave on ${hostName}" in out, out
        tip = dave_tip()
        assert as_git(
            "git -C /var/lib/forge/secrets.git -c gpg.ssh.allowedSignersFile=${fixtures}/dave_signers"
            " log -1 --format=%G? user/dave"
        ).strip() == "G"

        # A branch whose newest commit nobody trusted signed is built on by no one.
        as_git("git -C /home/git/d fetch -q origin user/dave && git -C /home/git/d reset -q --hard FETCH_HEAD")
        dave("evil_sig", "all", "evil")
        status, out = forge.execute("printf x | su git -c '${daveFleet} password ${hostName} --password-stdin' 2>&1")
        assert status != 0 and "signed neither by your signing key" in out, out
        force_dave(tip)

        # The host takes it all from his branch.
        sync_ok()
        fleet.fail("test -e /var/lib/fleet/user-problems")
        assert f"user dave {tip}\n" in manifest(), manifest()
        hashed = dave_password().strip()
        assert hashed.startswith("$y$"), hashed
        salt = "$".join(hashed.split("$")[2:4])
        forge.succeed(f"test \"$(printf %s dave-secret | mkpasswd -m yescrypt -S '{salt}' -s)\" = '{hashed}'")
        assert dave_value("secrets", "token") == "tok-123"
        assert "junk" not in fleet.succeed(
            "SOPS_AGE_KEY_FILE=/var/lib/fleet/age.key sops -d /var/lib/fleet/secrets/users/dave/${hostName}.yaml"
        )
        assert "dave_login@test" in dave_value("authorized_keys")

    with subtest("33: the host hands dave his secret, and him alone, and his key logs in"):
        # The stand-in switches of the sync tests left /run/current-system on a
        # directory of their own; this is the system that is really running.
        fleet.succeed("ln -sfn $(readlink -f /run/booted-system) /run/current-system")
        fleet.succeed("/run/booted-system/specialisation/dave-own/bin/switch-to-configuration test")
        out = fleet.succeed("stat -c '%U %a' /run/secrets/users/dave/all").strip()
        assert out == "dave 400", f"dave's file is '{out}', expected 'dave 400'"
        assert fleet.succeed("runuser -u dave -- fleet-user-secret token") == "tok-123\n"
        # Another name, and another user, get nothing.
        status, out = fleet.execute("runuser -u dave -- fleet-user-secret nothing 2>&1")
        assert status == 1 and "there is no secret named nothing" in out, (status, out)
        status, out = fleet.execute("runuser -u alice -- fleet-user-secret token 2>&1")
        assert status == 1 and "tok-123" not in out and "is not there" in out, (status, out)
        fleet.fail("runuser -u alice -- cat /run/secrets/users/dave/all")
        # The key he added with fleet user keys logs in as him, and only as him.
        out = forge.succeed(ssh_login.format(key="dave_login", user="dave", command="/run/current-system/sw/bin/fleet-user-secret token")).strip()
        assert out == "tok-123", out
        status, _ = forge.execute(ssh_login.format(key="dave_login", user="alice", command="true"))
        assert status != 0, "dave's key logged in as alice"
  '';
}
