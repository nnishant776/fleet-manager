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

      log() { echo "config-auto-update: $*"; }

      # Why the last run stopped, for the notice every account gets
      # (fleet/sync-status.nix): what went wrong, in words for whoever is at
      # the machine, and since when. A run that ends well removes it. A fetch
      # that fails leaves it as it is, since the next run simply retries.
      status=/var/lib/fleet-sync/status
      target=""
      stop() {  # stop KIND MESSAGE: record the problem and end the run
        since="$(date -Is)"
        if [ -r "$status" ] && grep -qxF "kind=$1" "$status" && grep -qxF "commit=$target" "$status"; then
          since="$(sed -n 's/^since=//p' "$status")"
        fi
        tmp="$(mktemp "$status.XXXXXX")"
        printf 'kind=%s\ncommit=%s\nsince=%s\nmessage=%s\n' "$1" "$target" "$since" "$2" > "$tmp"
        chmod 0644 "$tmp"
        mv -f "$tmp" "$status"
        exit 1
      }${lib.optionalString secretsOn ''

        # The snapshot of the secrets the running system was last activated
        # with. Activation is what decrypts them into /run/secrets, so a
        # snapshot that moved while the configuration did not needs one more
        # activation. A run that fails at it leaves the record as it was, and
        # the next run tries again.
        applied=/var/lib/fleet-sync/secrets-applied
        snapshot() { readlink ${lib.escapeShellArg config.conf.secrets.statePath}/secrets 2>/dev/null || true; }
        record_applied() {
          install -d -m 0755 /var/lib/fleet-sync
          snapshot > "$applied.tmp" && chmod 0644 "$applied.tmp" && mv -f "$applied.tmp" "$applied"
        }

        # Secrets first: a host whose snapshot cannot be refreshed or verified
        # must not go on to build and activate against a stale one.
        rc=0
        ${lib.getExe secretsSync.sync} sync ${secretsArgs} \
          --repo ${lib.escapeShellArg config.conf.secrets.repo.url} \
          --ref ${lib.escapeShellArg config.conf.secrets.repo.ref} \
          --signers ${signers} || rc=$?
        if [ "$rc" -ne 0 ]; then
          log "secrets sync failed with code $rc"
          case "$rc" in
            10) exit 1 ;;  # the fetch failed; the next run retries
            11) stop secrets-signature "The latest change to this machine's secrets is not signed by a trusted key, so the machine is not updating. It keeps running its current configuration." ;;
            16) stop secrets-rollback "The latest change to this machine's secrets does not follow the one it last accepted, so it is not updating; it could be an attempt to bring back old secrets. It keeps running its current configuration. If the history was rewritten on purpose, remove /var/lib/fleet/accepted on this machine to accept it." ;;
            *)  stop secrets "This machine could not refresh or verify its secrets, so it is not updating. It keeps running its current configuration." ;;
          esac
        fi
      ''}

      if [ ! -d "$repo/.git" ]; then
        # The first-boot clone makes it whenever it is missing, and keeps
        # trying until it can, so a machine whose checkout was lost, whatever
        # the cause, mends itself.
        log "no git checkout at $repo; starting the first-boot clone to make it"
        systemctl start --no-block fleet-bootstrap.service || log "could not start fleet-bootstrap"
        stop checkout "The configuration checkout at $repo is missing, so this machine cannot update until it is made again."
      fi

      # The checkout is evaluated as root; whoever can write it owns the machine.
      owner="$(stat -c %U "$repo")"
      mode="$(stat -c %a "$repo")"
      if [ "$owner" != root ] || [ $(( 8#$mode & 8#022 )) -ne 0 ]; then
        log "refusing: $repo is owned by $owner with mode $mode; it must be root-owned and not group- or world-writable"
        stop checkout "The configuration checkout at $repo has the wrong owner or permissions, so this machine will not update from it."
      fi

      # Fleet machines pull. The push URL is set to something that cannot
      # work, on every run, so a stray `git push` from this checkout fails.
      # The real control is the read-only credential the machine holds.
      git -C "$repo" remote set-url --push origin no_push
      # And it fetches from the URL its configuration names, set on every run,
      # so a change of repository reaches the checkout: the next fetch comes
      # from the new one, and resetLocalChanges moves the checkout over, even
      # to an unrelated history. What it fetches is verified as always.
      git -C "$repo" remote set-url origin ${lib.escapeShellArg cfg.repo.url}
      # Only the branch it tracks, even for a git fetch run by hand: with
      # lib.mkFleet's hostBranches, the other branches hold other hosts'
      # definitions.
      git -C "$repo" config remote.origin.fetch "+refs/heads/$ref:refs/remotes/origin/$ref"
      ${(import ./config-fetch.nix { inherit config lib; }).line}

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
        stop signature "The latest configuration change is not signed by a trusted key, so this machine is not applying it. It keeps running its current configuration."
      fi

      # A signature says who made a commit, not that it is the latest: anyone
      # who can push could serve an old signed commit again and take the
      # machine back to it. So the machine remembers the last commit it
      # accepted, and takes only that commit or one that follows it.
      # That holds when the configuration moves to another repository too: the
      # record never starts over by itself, since right after a move anyone
      # who can push to the new repository could serve an old commit of it. A
      # new URL for the same history passes as it is. A move to another
      # history stops until the record is removed on the machine, right after
      # the switch that moved it. The URL kept beside the record only tells
      # the two apart.
      accepted=/var/lib/fleet-sync/accepted
      url=${lib.escapeShellArg cfg.repo.url}
      if [ -s "$accepted" ]; then
        last="$(cat "$accepted")"
        if ! git -C "$repo" merge-base --is-ancestor "$last" "$target" 2>/dev/null; then
          if [ -s "$accepted.url" ] && [ "$(cat "$accepted.url")" != "$url" ]; then
            log "refusing: $target on $ref of $url does not follow $last, the last commit this machine accepted, from $(cat "$accepted.url")"
            stop moved "This machine's configuration now comes from $url, whose history does not include the commit it last applied, from $(cat "$accepted.url"), so it is not applying it. It keeps running its current configuration. If the move was on purpose, remove /var/lib/fleet-sync/accepted on this machine right after the switch that moved it; its next sync then starts from the newest commit there."
          fi
          log "refusing: $target on $ref does not follow $last, the last commit this machine accepted"
          stop rollback "The latest configuration change does not follow the one this machine last applied, so it is not applying it; it could be an attempt to take the machine back to an older configuration. It keeps running its current configuration. If the history was rewritten on purpose, remove /var/lib/fleet-sync/accepted on this machine to accept it."
        fi
      fi

      ${if au.resetLocalChanges then ''
        git -C "$repo" reset --hard "$target"
      '' else ''
        if ! git -C "$repo" merge --ff-only "$target"; then
          log "$repo has diverged from origin/$ref and cannot fast-forward"
          log "this machine is parked on a revision no upstream describes"
          stop diverged "The configuration checkout at $repo has changes of its own and cannot follow $ref, so this machine is not updating. It keeps running its current configuration."
        fi
      ''}

      # The checkout belongs to the fleet: it is the verified commit and
      # nothing else. HEAD is detached at it, origin is the only remote, and
      # it holds only the branch it tracks, so what a machine set up by hand
      # or moved from another repository left behind goes. Local branches go
      # too when local changes do. Best effort: a failure is logged, and
      # never stops the update.
      tidy() {
        git -C "$repo" checkout -q --detach "$target" || return 1
        git -C "$repo" remote | while read -r r; do
          [ "$r" = origin ] || git -C "$repo" remote remove "$r" || exit 1
        done || return 1
        # --no-deref: origin/HEAD is a symbolic ref, and deleting through it
        # would delete the branch it names.
        git -C "$repo" for-each-ref --format='%(refname)' refs/remotes/origin | while read -r r; do
          [ "$r" = "refs/remotes/origin/$ref" ] || git -C "$repo" update-ref --no-deref -d "$r" || exit 1
        done || return 1${lib.optionalString au.resetLocalChanges ''

        git -C "$repo" for-each-ref --format='%(refname:short)' refs/heads | while read -r b; do
          git -C "$repo" branch -q -D "$b" || exit 1
        done || return 1''}
      }
      tidy || log "could not tidy $repo; it stays at $target"

      # Recorded now, before the build: the commit is verified and checked
      # out, and a build that fails is fixed by a newer commit on top of it.
      printf '%s\n' "$target" > "$accepted.tmp" && chmod 0644 "$accepted.tmp" && mv -f "$accepted.tmp" "$accepted"
      printf '%s\n' "$url" > "$accepted.url.tmp" && chmod 0644 "$accepted.url.tmp" && mv -f "$accepted.url.tmp" "$accepted.url"
      after="$(git -C "$repo" rev-parse HEAD)"
      log "revision $before -> $after (tracking $ref)"${lib.optionalString secretsOn ''

        # A secret the new configuration declares but the snapshot lacks would
        # fail at activation and trigger a rollback; refuse before building.
        rc=0
        ${lib.getExe secretsSync.sync} check ${secretsArgs} --config "$repo" || rc=$?
        if [ "$rc" -ne 0 ]; then
          log "secrets check failed with code $rc"
          stop secrets-missing "The latest configuration needs a secret this machine does not have, so it is not being applied. The machine keeps running its current configuration."
        fi
      ''}

      if ! newSystem="$(nix build --no-link --print-out-paths \
            "$repo#nixosConfigurations.$host.config.system.build.toplevel")"; then
        log "build of $after failed — running system left untouched"
        stop build "The latest configuration does not build on this machine, so it keeps running its current one."
      fi

      running="$(readlink -f /run/current-system)"
      if [ "$newSystem" = "$running" ]; then${lib.optionalString secretsOn ''

        if [ "$(snapshot)" != "$(cat "$applied" 2>/dev/null || true)" ]; then
          # The configuration is the same and the secrets are not: the running
          # system is activated again, so they reach /run/secrets and the units
          # that use them restart. test, not switch: the boot entry is right.
          log "secrets changed to $(snapshot); activating the running system again"
          if ! "$running/bin/switch-to-configuration" test; then
            log "activation with the new secrets failed"
            stop activation "This machine could not start with its new secrets, so some services may still use the old ones. It keeps running its current configuration."
          fi
          record_applied
          log "running $after with the secrets of $(snapshot)"
          rm -f "$status"
          exit 0
        fi''}
        log "already running the configuration at $after"
        rm -f "$status"
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
            stop activation "The latest configuration failed to start, so this machine went back to its previous one."
          else
            log "ROLLBACK FAILED; the system may be in a mixed state"
            stop activation "The latest configuration failed to start, and going back to the previous one failed too. This machine may be partly updated."
          fi
        '' else ''
          log "activation failed at $after and rollbackOnFailure is off —"
          log "leaving the system as it is, which may be partially activated"
          stop activation "The latest configuration failed to start. This machine may be partly updated."
        ''}
      fi${lib.optionalString secretsOn ''

      record_applied''}
      rm -f "$status"

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
          assertion = !(import ./config-fetch.nix { inherit config lib; }).ssh
            || config.conf.secrets.repo.url != null;
          message =
            "conf.fleet.repo.url is an SSH address, which this host would fetch with its"
            + " own configuration key, but conf.secrets.repo.url is null, so it has no"
            + " identity and no key. Set conf.secrets.repo.url, or give an https://"
            + " address for a public repository.";
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

    systemd.services.config-auto-update = {
      description = "Sync ${cfg.localPath} from ${cfg.repo.ref} and rebuild";
      documentation = [ "https://github.com/nnishant776/fleet-manager" ];

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
        # /var/lib/fleet-sync, readable by every account, holds the status
        # file the notice in fleet/sync-status.nix reads, and the last commit
        # this machine accepted.
        StateDirectory = "fleet-sync";
        StateDirectoryMode = "0755";
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
