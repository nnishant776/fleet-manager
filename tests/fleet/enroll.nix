# Test of `fleetmgr host enroll` on a running host that has a checkout in
# /etc/nixos but no secrets identity yet: the case where the first-boot clone
# never runs, so the identity has to be made by hand. A `forge` node serves a
# fresh, signed secrets repository over SSH and also plays the maintainer's
# machine; `newhost` is built from tests/fleet/enroll-host. The checks:
# - fleetmgr logs in as admin through a one-key agent, creates the identity,
#   copies the enrollment file and checks it, enrolls the host with a signed
#   commit, pushes, and registers the deploy key;
# - the host can then fetch, verify and decrypt its secrets with that key;
# - a second run finds everything done and changes nothing;
# - the script enroll runs after a host's first switch makes /etc/nixos the
#   configuration's checkout, whether it held a checkout of something else or
#   plain files, with a stub in place of the first-boot clone, fails clearly
#   when the clone fails, and deletes nothing on a host without the unit;
# - the switch of a host moving from another repository is a trial, after
#   its secrets are fetched: a new system that stops sshd is not confirmed,
#   and the host goes back by itself; one that lets support in is confirmed
#   and made the boot default. The VM cannot evaluate a real configuration,
#   so a stand-in flake describes newhost's as the steps read it, and its
#   systems re-activate the real one, which puts the fetched secrets in place.
# Run it with `nix build .#checks.x86_64-linux.fleet-enroll`.
{ pkgs, inputs, self }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "newhost"; hostDir = ./enroll-host; };
  remakeScript = ../../tools/fleetmgr/flows/remake-etc-nixos.sh;

  fixtures = pkgs.runCommand "fleet-enroll-fixtures" {
    nativeBuildInputs = with pkgs; [ age openssh git ];
  } ''
    export HOME="$TMPDIR/home"
    mkdir -p "$HOME" "$out"
    age-keygen -o "$out/recovery.key" 2>/dev/null
    for key in signing admin support; do
      ssh-keygen -q -t ed25519 -N "" -C "$key@test" -f "$out/$key"
    done
    echo "maintainer@test namespaces=\"git\" $(cut -d' ' -f1,2 "$out/signing.pub")" > "$out/allowed_signers"

    signed_git() {
      git -c gpg.format=ssh -c "user.signingkey=$out/signing" -c commit.gpgsign=true \
        -c user.name=maintainer -c user.email=maintainer@test "$@"
    }

    # What fleetmgr setup caches for the maintainer's tools.
    age-keygen -y "$out/recovery.key" > "$out/recovery.pub"

    # A secrets repository as fleetmgr-secrets starts: main holds a README, and
    # there is no host branch yet.
    git init -q --bare -b main "$out/secrets.git"
    git init -q -b main "$TMPDIR/secrets"
    (
      cd "$TMPDIR/secrets"
      echo "# Secrets" > README.md
      printf '*.enroll\n' > .gitignore
      git add -A
      signed_git commit -q -m "fleetmgr-secrets, empty"
      git push -q "$out/secrets.git" main
    )
  '';

  secretsUrl = "ssh://git@forge/var/lib/forge/secrets.git";

  # newhost's configuration as the steps of a switch read it, with a system
  # that the switch builds: a directory, ./system, the same on every node.
  standInFlake = pkgs.writeText "flake.nix" ''
    {
      outputs = _: {
        nixosConfigurations.newhost.config = {
          conf.fleet.repo.url = "ssh://git@forge/var/lib/forge/config.git";
          conf.fleet.signing.allowedSignersFile = ./allowed_signers;
          conf.secrets.repo.url = "${secretsUrl}";
          conf.secrets.repo.ref = "host/newhost";
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
  # A stand-in system's activation: the real system's again (whose path the
  # test puts in /srv/real-system), then what the case does, then it points
  # /run/current-system at itself. boot changes nothing.
  standInSwitch = name: action: pkgs.writeScript "switch-to-configuration-${name}" ''
    #!/bin/sh
    c=${pkgs.coreutils}/bin
    here=$("$c/dirname" "$("$c/dirname" "$("$c/readlink" -f "$0")")")
    case "$1" in test|switch) ;; *) exit 0 ;; esac
    "$("$c/cat" /srv/real-system)/bin/switch-to-configuration" test || exit
    ${action}
    "$c/ln" -sfn "$here" /run/current-system
  '';
  goodSwitch = standInSwitch "good" "";
  badSwitch = standInSwitch "bad" "${pkgs.systemd}/bin/systemctl stop sshd.service";
in
pkgs.testers.runNixOSTest {
  name = "fleet-enroll";

  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.forge = { pkgs, ... }: {
    services.openssh.enable = true;
    # As on a maintainer's machine: fleetmgr evaluates flakes with plain nix.
    nix.settings.experimental-features = [ "nix-command" "flakes" ];
    users.users.git.isNormalUser = true;
    environment.systemPackages = [ pkgs.git self.packages.${pkgs.stdenv.hostPlatform.system}.fleetmgr ];

    systemd.services.forge-repos = {
      description = "Serve the fixture secrets repository, with deploy keys registered in a file";
      wantedBy = [ "multi-user.target" ];
      before = [ "sshd.service" ];
      requiredBy = [ "sshd.service" ];
      serviceConfig.Type = "oneshot";
      script = ''
        mkdir -p /var/lib/forge /home/git/.ssh
        cp -r --no-preserve=mode ${fixtures}/secrets.git /var/lib/forge/
        touch /home/git/.ssh/authorized_keys
        chmod 700 /home/git/.ssh
        chmod 600 /home/git/.ssh/authorized_keys
        chown -R git:users /var/lib/forge /home/git/.ssh
      '';
    };
  };

  nodes.newhost = { config, ... }: {
    imports = hostNode.modules ++ [{
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;
      # The trial switch activates the running system again to go back,
      # which tests leave out by default.
      system.switch.enable = true;

      # The host has secrets, so the keys of admin and support belong in their
      # files on the host's branch. That branch does not exist until enroll
      # makes it, and enroll logs in first, so the admin key is also offered
      # from a file of the test's own, to sshd and to sudo.
      conf.users.accounts.admin = {
        secrets.authorizedKeys = true;
        secrets.sudoKeys = true;
      };
      # Enroll logs in as admin before the host has a branch to take its keys
      # from, so the test offers admin's key from a file of its own: per
      # account (%u), as every key file must be, and a copy, not a link into
      # the store: sshd refuses a key file whose path is group-writable.
      environment.etc."test-keys/authorized/admin" = { source = "${fixtures}/admin.pub"; mode = "0444"; };
      services.openssh.authorizedKeysFiles = lib.mkAfter [ "/etc/test-keys/authorized/%u" ];
      security.pam.sshAgentAuth.authorizedKeysFiles = lib.mkAfter [ "/etc/test-keys/authorized/%u" ];
      conf.fleet = {
        repo.url = "ssh://git@forge/var/lib/forge/config.git";
        signing.allowedSignersFile = "${fixtures}/allowed_signers";
        support.keysFromSecrets = true;
      };
      conf.secrets.repo.url = secretsUrl;

      # The test makes the checkout itself and runs the secrets sync by hand.
      systemd.services.fleetmgr-bootstrap.enable = false;
      systemd.timers.config-auto-update.enable = false;

      # Root reaches the forge with the host's deploy key, once registered.
      programs.ssh.extraConfig = ''
        Host forge
          IdentityFile /var/lib/fleet/secrets_deploy_key
          IdentitiesOnly yes
          StrictHostKeyChecking accept-new
      '';
    }];
  };

  testScript = ''
    start_all()
    forge.wait_for_unit("sshd.service")
    newhost.wait_for_unit("sshd.service")

    # A running host with a checkout, as a Pi host had: the first-boot
    # clone never runs, so no identity exists.
    newhost.succeed(
        "mkdir -p /etc/nixos && git -C /etc/nixos init -q",
        "git -C /etc/nixos -c user.name=x -c user.email=x@test commit -q --allow-empty -m local",
    )
    newhost.fail("test -e /var/lib/fleet/age.key")

    # The forge as the maintainer's machine: keys, the recovery recipient as
    # fleetmgr setup caches it, a signing clone, the host's key known.
    forge.succeed(
        "install -o git -m 600 ${fixtures}/recovery.key ${fixtures}/signing ${fixtures}/admin /home/git/",
        "ssh-keyscan -t ed25519 newhost >> /home/git/.ssh/known_hosts 2>/dev/null",
        "chown git /home/git/.ssh/known_hosts",
        "su git -c 'mkdir -p ~/.local/state/fleet && cp ${fixtures}/recovery.pub ~/.local/state/fleet/recovery.pub'",
        "su git -c 'git clone -q file:///var/lib/forge/secrets.git /home/git/w"
        " && git -C /home/git/w config user.name maintainer"
        " && git -C /home/git/w config user.email maintainer@test"
        " && git -C /home/git/w config gpg.format ssh"
        " && git -C /home/git/w config user.signingKey /home/git/signing"
        " && git -C /home/git/w config commit.gpgSign true"
        " && git -C /home/git/w config gpg.ssh.allowedSignersFile ${fixtures}/allowed_signers'",
    )

    def enroll():
        # Bounded and printed, so that a step that hangs shows where.
        status, out = forge.execute(
            "cd /home/git && timeout 180 su git -c 'SOPS_AGE_KEY_FILE=/home/git/recovery.key"
            " fleetmgr host enroll newhost --flake /var/empty --key /home/git/admin"
            " --repo /home/git/w --recovery \"\" --sync=false --yes"
            " --register-with \"cat >> /home/git/.ssh/authorized_keys\"' 2>&1"
        )
        print(out)
        assert status == 0, f"fleetmgr host enroll exited {status}"
        return out

    with subtest("fleetmgr enrolls a running host without an identity"):
        out = enroll()
        for want in ["has no identity yet", "created under /var/lib/fleet", "checksum",
                     "committed: host-enroll: newhost", "stopped the one-key agent"]:
            assert want in out, f"output lacks {want!r}:\n{out}"
        newhost.succeed("test -f /var/lib/fleet/age.key -a -f /var/lib/fleet/secrets_deploy_key")
        # On the host's own branch, which main knows nothing of.
        forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git log --format=%s host/newhost' | grep -x 'host-enroll: newhost'")
        forge.fail("su git -c 'git -C /var/lib/forge/secrets.git log --format=%s main' | grep -x 'host-enroll: newhost'")
        forge.succeed(
            "su git -c 'git -C /var/lib/forge/secrets.git -c gpg.ssh.allowedSignersFile=${fixtures}/allowed_signers"
            " verify-commit host/newhost'"
        )
        key = newhost.succeed("cut -d' ' -f1,2 /var/lib/fleet/secrets_deploy_key.pub").strip()
        forge.succeed(f"grep -qF '{key}' /home/git/.ssh/authorized_keys")

    with subtest("the host then fetches, verifies and decrypts its secrets"):
        newhost.succeed(
            "fleetmgr-secrets-sync sync --state /var/lib/fleet --host newhost"
            " --repo ${secretsUrl} --ref host/newhost --signers ${fixtures}/allowed_signers"
        )
        newhost.succeed("test -f /var/lib/fleet/secrets/hosts/newhost.yaml")

    with subtest("a second run finds everything done"):
        before = forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/newhost'")
        out = enroll()
        for want in ["skipped: it has one", "skipped: it is enrolled already, with this identity",
                     "skipped: nothing to push"]:
            assert want in out, f"output lacks {want!r}:\n{out}"
        after = forge.succeed("su git -c 'git -C /var/lib/forge/secrets.git rev-parse host/newhost'")
        assert before == after, "a second run changed the secrets repository"

    with subtest("/etc/nixos is replaced by the configuration's checkout, whatever it held"):
        # What enroll runs on a host after its first switch, on scratch
        # directories in place of /etc/nixos and /var/lib/fleet-sync. This test
        # keeps the host's own fleetmgr-bootstrap off and has no configuration
        # repository on the forge, and a system built here records no revision
        # to accept, so the unit cannot make a real clone: a stub systemctl
        # does what the unit does when it is started and finds no checkout
        # (its condition), which is the one thing the script relies on.
        newhost.succeed(
            "git init -q --bare -b main /srv/config.git",
            "git clone -q /srv/config.git /tmp/c",
            "git -C /tmp/c -c user.name=x -c user.email=x@test commit -q --allow-empty -m one",
            "git -C /tmp/c push -q origin HEAD:main",
            "mkdir -p /tmp/stub",
            "printf '%s\\n' '#!/bin/sh'"
            " 'if [ \"$1\" = cat ]; then [ -z \"$NO_UNIT\" ]; exit; fi'"
            " '[ -d \"$DIR/.git\" ] && exit 0'"
            " 'rm -rf \"$DIR\"'"
            " 'git clone -q --branch main \"$URL\" \"$DIR\"'"
            " 'mkdir -p \"$STATE\"'"
            " 'git -C \"$DIR\" rev-parse HEAD > \"$STATE/accepted\"'"
            " 'echo \"$URL\" > \"$STATE/accepted.url\"' > /tmp/stub/systemctl",
            "chmod +x /tmp/stub/systemctl",
        )
        one = newhost.succeed("git -C /srv/config.git rev-parse main").strip()
        url = "file:///srv/config.git"
        env = f"PATH=/tmp/stub:$PATH URL={url} DIR=/tmp/etc-nixos STATE=/tmp/fleet-sync"
        git = "git -C /tmp/etc-nixos"

        def check_replaced():
            assert newhost.succeed(f"{git} rev-parse HEAD").strip() == one
            assert newhost.succeed(f"{git} remote").strip() == "origin"
            assert newhost.succeed(f"{git} remote get-url origin").strip() == url
            assert newhost.succeed(f"{git} branch --format=\"%(refname:short)\"").strip() == "main"
            newhost.fail("test -e /tmp/etc-nixos/mine.nix")
            assert newhost.succeed("cat /tmp/fleet-sync/accepted").strip() == one
            assert newhost.succeed("cat /tmp/fleet-sync/accepted.url").strip() == url

        # A checkout of something else, with a branch of its own, files that
        # were never committed, and an older record of what the sync accepted.
        newhost.succeed(
            "git init -q -b mine /tmp/etc-nixos",
            "git -C /tmp/etc-nixos -c user.name=x -c user.email=x@test commit -q --allow-empty -m mine",
            "git -C /tmp/etc-nixos remote add origin https://example.org/mine.git",
            "touch /tmp/etc-nixos/mine.nix",
            "mkdir -p /tmp/fleet-sync && echo old > /tmp/fleet-sync/accepted && echo https://example.org/mine.git > /tmp/fleet-sync/accepted.url",
        )
        out = newhost.succeed(f"{env} sh ${remakeScript} {url} true /tmp/etc-nixos /tmp/fleet-sync")
        assert out.strip() == one, out
        check_replaced()

        # Plain files: the unit finds no checkout and replaces them.
        newhost.succeed("rm -rf /tmp/etc-nixos /tmp/fleet-sync", "mkdir /tmp/etc-nixos", "touch /tmp/etc-nixos/mine.nix")
        out = newhost.succeed(f"{env} sh ${remakeScript} {url} false /tmp/etc-nixos /tmp/fleet-sync")
        assert out.strip() == one, out
        check_replaced()

        # A clone that fails leaves the script failing, and says the unit goes on trying.
        newhost.succeed("rm -rf /tmp/etc-nixos /tmp/fleet-sync", "mkdir /tmp/etc-nixos", "touch /tmp/etc-nixos/mine.nix")
        status, out = newhost.execute(
            "PATH=/tmp/stub:$PATH URL=file:///srv/missing.git DIR=/tmp/etc-nixos STATE=/tmp/fleet-sync"
            " sh ${remakeScript} file:///srv/missing.git false /tmp/etc-nixos /tmp/fleet-sync 2>&1"
        )
        assert status != 0 and "keeps trying every 60 seconds" in out, out

        # A host without the unit could not make the checkout again, so
        # nothing is deleted.
        newhost.succeed("rm -rf /tmp/etc-nixos", "mkdir /tmp/etc-nixos", "touch /tmp/etc-nixos/mine.nix")
        status, out = newhost.execute(f"NO_UNIT=1 {env} sh ${remakeScript} {url} true /tmp/etc-nixos /tmp/fleet-sync 2>&1")
        assert status != 0 and "left as it is" in out, out
        newhost.succeed("test -e /tmp/etc-nixos/mine.nix")

    # The switch of a host that moves here from another repository.
    real = newhost.succeed("readlink -f /run/current-system").strip()
    # A test VM boots from the store, with no system profile yet.
    profile = newhost.succeed("readlink /nix/var/nix/profiles/system || true").strip()
    for node in (forge, newhost):
        node.succeed(
            "readlink -f /run/current-system > /srv/real-system",
            "for c in good bad; do mkdir -p /srv/$c/system/bin"
            " && echo '{\"nodes\":{\"root\":{}},\"root\":\"root\",\"version\":7}' > /srv/$c/flake.lock && cp ${standInFlake} /srv/$c/flake.nix && cp ${fixtures}/allowed_signers /srv/$c/; done",
            "install -m 0755 ${goodSwitch} /srv/good/system/bin/switch-to-configuration",
            "install -m 0755 ${badSwitch} /srv/bad/system/bin/switch-to-configuration",
            # The rest of the real system, so that the test driver still finds
            # its tools under /run/current-system/sw.
            "for c in good bad; do for f in \"$(cat /srv/real-system)\"/*; do"
            " [ \"''${f##*/}\" = bin ] || ln -s \"$f\" /srv/$c/system/; done; done",
        )
    newhost.succeed("git -C /etc/nixos remote add origin https://example.org/old.git")
    # The keys the new system's accounts take from the secrets, and support's
    # private key, with which fleetmgr logs in to confirm the new system.
    forge.succeed(
        "install -o git -m 644 ${fixtures}/admin.pub ${fixtures}/support.pub /home/git/",
        "install -o git -m 600 ${fixtures}/support /home/git/.ssh/id_ed25519",
        "cd /home/git && su git -c 'SOPS_AGE_KEY_FILE=/home/git/recovery.key fleetmgr secrets user-keys admin newhost"
        " --authorized-keys /home/git/admin.pub --sudo-keys /home/git/admin.pub --repo /home/git/w --recovery \"\" --yes'",
        "cd /home/git && su git -c 'SOPS_AGE_KEY_FILE=/home/git/recovery.key fleetmgr secrets user-keys support newhost"
        " --authorized-keys /home/git/support.pub --repo /home/git/w --recovery \"\" --yes'",
    )

    def switch_to(flake):
        status, out = forge.execute(
            "cd /home/git && timeout 600 su git -c 'SOPS_AGE_KEY_FILE=/home/git/recovery.key"
            f" fleetmgr host enroll newhost --flake {flake} --switch-to {flake} --trial-minutes 1 --key /home/git/admin"
            " --repo /home/git/w --recovery \"\" --sync=false --yes"
            " --register-with \"cat >> /home/git/.ssh/authorized_keys\"' 2>&1"
        )
        print(out)
        return status, out

    with subtest("a trial switch to a system that cuts off SSH is not confirmed, and goes back by itself"):
        status, out = switch_to("/srv/bad")
        assert status != 0, "a system support cannot reach was taken"
        for want in ["fetched, verified and decrypted", "armed: unless the new system is confirmed"]:
            assert want in out, f"output lacks {want!r}"
        assert "gone back already" in out or "goes back to the system it ran before by itself" in out, out
        assert "the new system is the boot default" not in out
        newhost.wait_until_succeeds(f"test \"$(readlink -f /run/current-system)\" = {real}", timeout=180)
        newhost.wait_for_unit("sshd.service")
        newhost.fail("systemctl is-active fleetmgr-trial-rollback.timer")
        assert newhost.succeed("readlink /nix/var/nix/profiles/system || true").strip() == profile, "the boot default moved"

    with subtest("a trial switch to a system support reaches is confirmed and made the boot default"):
        status, out = switch_to("/srv/good")
        for want in ["stopped the timer: the new system stays", "the new system is the boot default"]:
            assert want in out, f"output lacks {want!r}"
        # This host has no first-boot clone to make /etc/nixos the checkout
        # with, so the step after that stops, as it should here.
        assert status != 0 and "stopped at step" in out and "Make /etc/nixos the configuration" in out, out
        new = newhost.succeed("readlink -f /run/current-system").strip()
        assert new != real and new.startswith("/nix/store/"), new
        assert newhost.succeed("readlink -f /nix/var/nix/profiles/system").strip() == new
        newhost.fail("systemctl is-active fleetmgr-trial-rollback.timer")
        # It stays past the trial's minute.
        newhost.sleep(75)
        assert newhost.succeed("readlink -f /run/current-system").strip() == new
  '';
}
