# Activates a new NixOS system for a trial. fleetmgr runs it as root on the
# host, with the system's store path and how many minutes the trial lasts:
#
#   sh trial-switch.sh NEW MINUTES
#
# First it arms a timer, fleetmgr-trial-rollback, that activates the system
# running now again after MINUTES, unless fleetmgr logs in to the new system
# and stops it. Then it activates NEW with switch-to-configuration test, as a
# unit, so that a dropped connection cannot cut the activation in half. test
# leaves the boot default as it is, so a reboot brings back the system that
# ran before too. An activation that fails goes back at once.
set -eu
new=$1 minutes=$2
path=/run/wrappers/bin:/run/current-system/sw/bin
old=$(readlink -f /run/current-system)

if [ ! -x "$new/bin/switch-to-configuration" ]; then
  echo "$new is not a NixOS system: it has no bin/switch-to-configuration" >&2
  exit 2
fi

# logind stops a user's systemd ten seconds after their last session ends,
# and fleetmgr's own logins to the host end just before this runs. An
# activation that reaches a user's systemd while it stops, or just after,
# fails though nothing is wrong with the system: switch-to-configuration
# cannot reload that user's units ("Failed to get runtime directory", "Failed
# to get GID"). So, as the sync does (modules/linux/fleet/sync.nix), an
# activation first waits for those to stop; and one that fails is tried once
# more after waiting again, before it counts and the host goes back.
pinned() { # pinned UID: whether a session keeps the user's systemd running
  for s in $(loginctl show-user "$1" -p Sessions --value 2>/dev/null); do
    case "$(loginctl show-session "$s" -p Class --value 2>/dev/null)" in
      manager | manager-early | background-light | none | "") ;;
      *) return 0 ;;
    esac
  done
  return 1
}
stopping() { # the users whose systemd is stopping or about to
  loginctl list-users --no-legend 2>/dev/null | while read -r uid name _; do
    systemctl is-active --quiet "user@$uid.service" || continue
    [ "$(loginctl show-user "$uid" -p Linger --value 2>/dev/null)" = no ] || continue
    pinned "$uid" && continue
    echo "$name"
  done
  systemctl list-units --plain --no-legend --state=deactivating 'user@*.service' | cut -d' ' -f1
}
settle() { # wait, for two minutes at most, until none is
  waited=0
  while users=$(stopping | tr '\n' ' ') && [ -n "$users" ]; do
    if [ "$waited" -ge 120 ]; then
      echo "activating while the systemd of ${users% } still stops"
      return 0
    fi
    [ "$waited" -gt 0 ] || echo "waiting for the systemd of ${users% } to stop, after the last session ended"
    sleep 2
    waited=$((waited + 2))
  done
}
activate() { # activate UNIT SYSTEM: SYSTEM's switch-to-configuration test, as UNIT
  settle
  systemctl reset-failed "$1" 2>/dev/null || true
  systemd-run --quiet --wait --collect --unit="$1" --setenv=PATH="$path" "$2/bin/switch-to-configuration" test
}

systemctl stop fleetmgr-trial-rollback.timer 2>/dev/null || true
systemctl reset-failed fleetmgr-trial-rollback.timer fleetmgr-trial-rollback.service \
  fleetmgr-trial-switch.service fleetmgr-trial-back.service 2>/dev/null || true
systemd-run --quiet --unit=fleetmgr-trial-rollback --on-active="${minutes}min" \
  --timer-property=AccuracySec=1s \
  --description="Go back to the system that ran before fleetmgr's trial switch" \
  --setenv=PATH="$path" "$old/bin/switch-to-configuration" test
echo "armed: unless the new system is confirmed, $(hostname) goes back to $old at $(date -d "now + $minutes min" +%H:%M:%S)"

s=0
activate fleetmgr-trial-switch "$new" || s=$?
if [ "$s" -ne 0 ]; then
  journalctl -u fleetmgr-trial-switch -o cat -n 15 --no-pager || true
  echo "the activation failed (code $s); a user's systemd may have stopped under it: activating once more" >&2
  s=0
  activate fleetmgr-trial-switch "$new" || s=$?
fi
journalctl -u fleetmgr-trial-switch -o cat -n 15 --no-pager || true
if [ "$s" -ne 0 ]; then
  echo "the activation failed (code $s): going back to $old now" >&2
  systemctl stop fleetmgr-trial-rollback.timer || true
  activate fleetmgr-trial-back "$old" || true
  exit 3
fi
echo "activated $new for a trial"
