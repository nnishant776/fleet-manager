# Periodic sync of conf.fleet.repo into the local checkout, followed by a
# rebuild from it.
#
# Build and activation are separate steps: a build failure leaves the running
# system untouched (nothing to roll back), while an activation failure means
# the profile already moved and needs a rollback. Collapsing both into one
# `nixos-rebuild switch` would make the two failure modes indistinguishable.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.fleet;
  au = cfg.autoUpdate;

  # Public keys only, copied into the store. /dev/null when unset, which the
  # assertion below only allows for a host with no upstream.
  signers =
    if cfg.signing.allowedSignersFile != null
    then "${cfg.signing.allowedSignersFile}"
    else "/dev/null";

  # Present only on a host that opted in to secrets; see fleet/secrets-sync.nix.
  secretsOn = config.conf.secrets.repo.url != null;
  secretsSync = import ../../../lib/secrets-sync.nix {
    inherit pkgs;
    nix = config.nix.package;
  };
  secretsArgs = lib.escapeShellArgs [
    "--state" config.conf.secrets.statePath
    "--host" config.conf.host.name
  ];

  syncScript = pkgs.writeShellApplication {
    name = "config-auto-update";
    runtimeInputs = [
      pkgs.git
      pkgs.nixos-rebuild
      config.nix.package
      pkgs.coreutils
      pkgs.systemd
      pkgs.openssh # ssh-keygen, which git uses to verify SSH signatures
    ];
    text = ''
      repo=${lib.escapeShellArg cfg.localPath}
      ref=${lib.escapeShellArg cfg.repo.ref}
      host=${lib.escapeShellArg config.conf.host.name}

      log() { echo "config-auto-update: $*"; }${lib.optionalString secretsOn ''

        # Secrets first: a host whose snapshot cannot be refreshed or verified
        # must not go on to build and activate against a stale one.
        rc=0
        ${lib.getExe secretsSync.sync} sync ${secretsArgs} \
          --repo ${lib.escapeShellArg config.conf.secrets.repo.url} \
          --ref ${lib.escapeShellArg config.conf.secrets.repo.ref} \
          --signers ${signers} || rc=$?
        if [ "$rc" -ne 0 ]; then
          log "secrets sync failed with code $rc"
          exit 1
        fi
      ''}

      if [ ! -d "$repo/.git" ]; then
        log "no git checkout at $repo — refusing to sync"
        log "os-install populates it; see conf.fleet.repo"
        exit 1
      fi

      # The checkout is evaluated as root; whoever can write it owns the machine.
      owner="$(stat -c %U "$repo")"
      mode="$(stat -c %a "$repo")"
      if [ "$owner" != root ] || [ $(( 8#$mode & 8#022 )) -ne 0 ]; then
        log "refusing: $repo is owned by $owner with mode $mode; it must be root-owned and not group- or world-writable"
        exit 1
      fi

      # Fleet machines pull. The push URL is set to something that cannot
      # work, on every run, so a stray `git push` from this checkout fails.
      # The real control is the read-only credential the machine holds.
      git -C "$repo" remote set-url --push origin no_push
      ${lib.optionalString (cfg.repo.deployKeySecret != null) ''
        export GIT_SSH_COMMAND="ssh -i ${config.sops.secrets.${cfg.repo.deployKeySecret}.path} -o IdentitiesOnly=yes"
      ''}

      # The network may not be up yet on a machine that does not wait for it
      # at boot, so the fetch is retried with a growing pause.
      attempt=0
      until git -C "$repo" fetch --prune origin "$ref"; do
        attempt=$((attempt + 1))
        if [ "$attempt" -ge 10 ]; then
          log "fetch of $ref failed after $attempt attempts"
          exit 1
        fi
        sleep $((attempt * 15))
      done
      target="$(git -C "$repo" rev-parse FETCH_HEAD)"
      before="$(git -C "$repo" rev-parse HEAD)"

      # Nothing is checked out, built or activated unless the fetched commit
      # carries an SSH signature from the signers file this configuration ships.
      if ! git -C "$repo" -c gpg.ssh.allowedSignersFile=${signers} verify-commit "$target" >/dev/null 2>&1; then
        log "refusing: commit $target on $ref is not signed by an allowed signer"
        exit 1
      fi

      ${if au.resetLocalChanges then ''
        git -C "$repo" reset --hard "$target"
      '' else ''
        if ! git -C "$repo" merge --ff-only "$target"; then
          log "$repo has diverged from origin/$ref and cannot fast-forward"
          log "this machine is parked on a revision no upstream describes"
          exit 1
        fi
      ''}

      after="$(git -C "$repo" rev-parse HEAD)"
      log "revision $before -> $after (tracking $ref)"${lib.optionalString secretsOn ''

        # A secret the new configuration declares but the snapshot lacks would
        # fail at activation and trigger a rollback; refuse before building.
        rc=0
        ${lib.getExe secretsSync.sync} check ${secretsArgs} --config "$repo" || rc=$?
        if [ "$rc" -ne 0 ]; then
          log "secrets check failed with code $rc"
          exit 1
        fi
      ''}

      if ! newSystem="$(nix build --no-link --print-out-paths \
            "$repo#nixosConfigurations.$host.config.system.build.toplevel")"; then
        log "build of $after failed — running system left untouched"
        exit 1
      fi

      running="$(readlink -f /run/current-system)"
      if [ "$newSystem" = "$running" ]; then
        log "already running the configuration at $after"
        exit 0
      fi

      # Target this generation by number, not `--rollback` ("one back"), which
      # would quietly downgrade the machine if the switch failed before moving
      # the profile.
      systemProfile=/nix/var/nix/profiles/system
      previousLink="$(basename "$(readlink "$systemProfile")")"
      previousGen="''${previousLink#system-}"
      previousGen="''${previousGen%-link}"
      log "activating $newSystem (current generation $previousGen)"

      if ! nixos-rebuild switch --flake "$repo#$host"; then
        ${if au.rollbackOnFailure then ''
          log "activation failed — returning to generation $previousGen"
          if nix-env -p "$systemProfile" --switch-generation "$previousGen" \
             && "$systemProfile/bin/switch-to-configuration" switch; then
            log "rolled back to generation $previousGen; this machine no longer"
            log "matches $after and will show as drift until upstream is fixed"
          else
            log "ROLLBACK FAILED; the system may be in a mixed state"
          fi
        '' else ''
          log "activation failed at $after and rollbackOnFailure is off —"
          log "leaving the system as it is, which may be partially activated"
        ''}
        exit 1
      fi

      # Reported, not acted on: rolling back on any degraded state would put a
      # machine with one pre-existing failed unit into a rollback loop on every
      # tick. A real health gate would need to compare against a baseline.
      state="$(systemctl is-system-running || true)"
      log "system state after switch: $state"
      if [ "$state" != "running" ]; then
        systemctl --failed --no-legend --plain || true
      fi

      ${lib.optionalString au.allowReboot ''
        booted="$(readlink -f /run/booted-system/kernel /run/booted-system/initrd 2>/dev/null || true)"
        built="$(readlink -f "$newSystem/kernel" "$newSystem/initrd" 2>/dev/null || true)"
        if [ "$booted" != "$built" ]; then
          log "kernel or initrd changed — rebooting"
          systemctl reboot
        fi
      ''}

      log "done at $after"
    '';
  };
  # null means "sync if this machine has an upstream at all", so a fleet host
  # converges by virtue of having been given a repository and a standalone
  # machine is left alone. An explicit value wins either way.
  enabled =
    if au.enable != null then au.enable else cfg.repo.url != null;
in
{
  config = lib.mkMerge [
    {
      assertions = [
        {
          assertion = cfg.repo.url == null || cfg.signing.allowedSignersFile != null;
          message =
            "conf.fleet.repo.url is set but conf.fleet.signing.allowedSignersFile"
            + " is null: a fleet host must verify what it fetches before building it.";
        }
        {
          assertion = cfg.repo.deployKeySecret == null || config.conf.secrets.repo.url != null;
          message =
            "conf.fleet.repo.deployKeySecret is set but conf.secrets.repo.url is null;"
            + " the key has to come from the host's file in the secrets repository.";
        }
        {
          assertion = cfg.repo.url == null || !(lib.hasPrefix "http://" cfg.repo.url);
          message =
            "conf.fleet.repo.url uses plain http://, which lets anyone on the path"
            + " serve configuration to this machine. Use https:// or ssh.";
        }
        {
          assertion = !(au.enable == true && cfg.repo.url == null);
          message =
            "conf.fleet.autoUpdate.enable is true but"
            + " conf.fleet.repo.url is null: there is nothing to sync from."
            + " Leave enable unset to have it follow whether a repository is"
            + " configured.";
        }
      ];
    }

    (lib.mkIf (enabled && cfg.repo.url != null && config.conf.platform == "nixos") {

    # Read-only deploy key for a private repository, root-only at
    # /run/secrets/<name>; the sync hands it to ssh above.
    sops.secrets = lib.mkIf (cfg.repo.deployKeySecret != null) {
      ${cfg.repo.deployKeySecret} = { };
    };

    systemd.services.config-auto-update = {
      description = "Sync ${cfg.localPath} from ${cfg.repo.ref} and rebuild";
      documentation = [ "https://github.com/nnishant776/nixos-config" ];

      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      # Required because this service is part of the configuration it
      # activates; otherwise switch-to-configuration restarts it mid-run and
      # the run is killed by its own success.
      restartIfChanged = false;

      # Not sandboxed further: this unit exists to replace the running system,
      # so it needs /nix, /etc, /boot and the profile writable, and ProtectHome
      # would break nix's cache under /root. PrivateTmp is the one thing that
      # costs nothing.
      serviceConfig = {
        Type = "oneshot";
        PrivateTmp = true;
        ExecStart = lib.getExe syncScript;
      };
    };

    systemd.timers.config-auto-update = {
      description = "Timer for ${cfg.localPath} configuration sync";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnCalendar = au.dates;
        RandomizedDelaySec = au.randomizedDelaySec;
        # Machines that were off at the scheduled time still converge.
        Persistent = true;
      };
    };
    })
  ];
}
