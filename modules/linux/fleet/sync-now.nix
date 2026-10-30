# `fleet-sync-now` starts the sync (fleet/sync.nix) at once, rather than at its
# timer. That needs root, and on these hosts a privileged account often has no
# password: its sudo works only through a forwarded SSH agent. Starting the
# sync is safe to allow without authentication. The sync applies only a commit
# signed by a trusted key that follows the one it accepted last, and refuses
# everything else, so the worst anyone can do is make it run more often. So
# accounts in `wheel`, which is what `privileged` gives, may run exactly this
# one command as root without a password. Nothing else changes: other accounts
# are told they cannot, and `systemctl start config-auto-update` still asks for
# authentication.
#
# As root it shows the unit's log while it runs, then what the machine runs
# against the newest commit of its branch, then what `fleet-sync-status` says.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.fleet;
  au = cfg.autoUpdate;
  # As in fleet/sync.nix: the command exists wherever the sync does.
  enabled = if au.enable != null then au.enable else cfg.repo.url != null;

  path = "/run/current-system/sw/bin/fleet-sync-now";
  sudo = "/run/wrappers/bin/sudo";

  syncNow = pkgs.writeShellApplication {
    name = "fleet-sync-now";
    runtimeInputs = [ pkgs.coreutils pkgs.git pkgs.systemd ];
    text = ''
      repo=${lib.escapeShellArg cfg.localPath}
      ref=${lib.escapeShellArg cfg.repo.ref}

      if [ "$(id -u)" -ne 0 ]; then
        if ${sudo} -n -l ${path} >/dev/null 2>&1; then
          exec ${sudo} -n ${path} "$@"
        fi
        echo "fleet-sync-now: only privileged accounts can start the sync; fleet-sync-status shows how updates are going" >&2
        exit 1
      fi

      echo "Syncing $repo from $ref now..."

      # The unit's log, as it is written. Stopped a moment after the unit
      # ends, so its last lines reach the journal and are shown first.
      journalctl -f -o cat -u config-auto-update.service --since now &
      follower=$!
      rc=0
      systemctl start config-auto-update.service || rc=$?
      sleep 1
      kill "$follower" 2>/dev/null || true
      wait "$follower" 2>/dev/null || true
      echo

      running="$(cat /etc/org/revision 2>/dev/null || true)"
      newest="$(git -C "$repo" rev-parse --verify -q "refs/remotes/origin/$ref" || true)"
      if [ -z "$running" ] || [ "$running" = unknown ]; then
        echo "The revision this machine runs is unknown."
      elif [ "$running" = "$newest" ]; then
        echo "This machine runs ''${running:0:12}, the newest commit of $ref."
      elif [ -n "$newest" ]; then
        echo "This machine runs ''${running:0:12}; $ref is at ''${newest:0:12}."
      else
        echo "This machine runs ''${running:0:12}; the newest commit of $ref is unknown."
      fi
      echo
      /run/current-system/sw/bin/fleet-sync-status

      [ "$rc" -eq 0 ] || exit 1
    '';
  };
in {
  config = lib.mkIf (enabled && cfg.repo.url != null && config.conf.platform == "nixos") {
    environment.systemPackages = [ syncNow ];

    security.sudo.extraRules = [
      {
        groups = [ "wheel" ];
        commands = [ { command = path; options = [ "NOPASSWD" ]; } ];
      }
    ];
  };
}
