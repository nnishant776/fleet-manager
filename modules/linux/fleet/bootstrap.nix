# First boot of a fleet machine: clone the configuration it was installed from
# into conf.fleet.localPath, at the revision the running system was built from,
# so that `git -C /etc/nixos rev-parse HEAD` describes the machine from the
# start. Runs until a checkout exists; after that the sync owns it.
{ config, lib, pkgs, inputs, ... }:
let
  cfg = config.conf.fleet;
  # null when built from a tree with uncommitted changes, which has no
  # revision anything could be checked out to.
  revision = inputs.self.rev or null;

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
  # Same file the configuration sync verifies against (see fleet/sync.nix).
  signers =
    if cfg.signing.allowedSignersFile != null
    then "${cfg.signing.allowedSignersFile}"
    else "/dev/null";

  bootstrap = pkgs.writeShellApplication {
    name = "fleet-bootstrap";
    runtimeInputs = [ pkgs.git pkgs.openssh pkgs.coreutils pkgs.systemd ];
    text = ''
      repo=${lib.escapeShellArg cfg.localPath}${lib.optionalString secretsOn ''

        # Secrets before the configuration: accounts and passwords come from
        # the snapshot, so nothing is cloned until it exists. Exiting here
        # leaves the unit's condition true and the restart policy retrying.
        rc=0
        ${lib.getExe secretsSync.sync} init ${secretsArgs} || rc=$?
        if [ "$rc" -eq 0 ]; then
          ${lib.getExe secretsSync.sync} sync ${secretsArgs} \
            --repo ${lib.escapeShellArg config.conf.secrets.repo.url} \
            --ref ${lib.escapeShellArg config.conf.secrets.repo.ref} \
            --signers ${signers} || rc=$?
        fi
        if [ "$rc" -ne 0 ]; then
          echo "fleet-bootstrap: secrets sync failed with code $rc; retrying"
          exit 1
        fi
      ''}
      ${import ./config-fetch.nix { inherit config lib; }}

      # Clone beside the target and move it into place, so a half-finished
      # clone never looks like a checkout, and an existing empty directory is
      # fine.
      tmp="$(mktemp -d "$(dirname "$repo")/.fleet-bootstrap.XXXXXX")"
      trap 'rm -rf "$tmp"' EXIT
      git clone --branch ${lib.escapeShellArg cfg.repo.ref} ${lib.escapeShellArg cfg.repo.url} "$tmp/checkout"
      installed=""
      ${if revision != null then ''
        if git -C "$tmp/checkout" checkout --quiet ${revision}; then
          installed=${revision}
        else
          echo "fleet-bootstrap: revision ${revision} not on ${cfg.repo.ref}; left at its tip, the sync will converge"
        fi
      '' else ''
        echo "fleet-bootstrap: this system was built from uncommitted changes; checkout left at the tip of ${cfg.repo.ref}"
      ''}
      # Fleet machines pull; see fleet/sync.nix.
      git -C "$tmp/checkout" remote set-url --push origin no_push

      if [ -d "$repo" ] && [ -n "$(ls -A "$repo")" ]; then
        echo "fleet-bootstrap: $repo exists and is not empty; refusing to replace it" >&2
        exit 1
      fi
      rmdir "$repo" 2>/dev/null || true
      mv "$tmp/checkout" "$repo"

      # The revision the system was installed from is the first commit this
      # machine accepted, so the sync refuses an older one from its very first
      # run; see fleet/sync.nix. A checkout left at the tip records nothing,
      # and the first sync trusts what it fetches.
      if [ -n "$installed" ]; then
        accepted=/var/lib/fleet-sync/accepted
        install -d -m 0755 /var/lib/fleet-sync
        printf '%s\n' "$installed" > "$accepted.tmp" && chmod 0644 "$accepted.tmp" && mv -f "$accepted.tmp" "$accepted"
      fi

      # The audit rules loaded at boot, before the checkout existed, so they
      # watch its path as a name only; loading them again watches everything
      # in it from now on (audit/auditd.nix).
      systemctl try-restart audit-rules-nixos.service || true
      echo "fleet-bootstrap: $repo at $(git -C "$repo" rev-parse HEAD)"
    '';
  };
in {
  config = lib.mkIf (cfg.repo.url != null && config.conf.platform == "nixos") {
    systemd.services.fleet-bootstrap = {
      description = "Clone the fleet configuration into ${cfg.localPath} on first boot";
      wantedBy = [ "multi-user.target" ];
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      unitConfig.ConditionPathExists = "!${cfg.localPath}/.git";
      # Part of the configuration it clones; restarting it on a switch would
      # be pointless once a checkout exists, and the condition skips it then.
      restartIfChanged = false;
      serviceConfig = {
        Type = "oneshot";
        ExecStart = lib.getExe bootstrap;
        # No network yet on a machine that does not wait for it: keep trying.
        Restart = "on-failure";
        RestartSec = 60;
      };
    };

    # The sync needs the checkout; on first boot it waits for the clone.
    systemd.services.config-auto-update.after = [ "fleet-bootstrap.service" ];
  };
}
