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

systemctl stop fleetmgr-trial-rollback.timer 2>/dev/null || true
systemctl reset-failed fleetmgr-trial-rollback.timer fleetmgr-trial-rollback.service \
  fleetmgr-trial-switch.service fleetmgr-trial-back.service 2>/dev/null || true
systemd-run --quiet --unit=fleetmgr-trial-rollback --on-active="${minutes}min" \
  --timer-property=AccuracySec=1s \
  --description="Go back to the system that ran before fleetmgr's trial switch" \
  --setenv=PATH="$path" "$old/bin/switch-to-configuration" test
echo "armed: unless the new system is confirmed, $(hostname) goes back to $old at $(date -d "now + $minutes min" +%H:%M:%S)"

s=0
systemd-run --quiet --wait --collect --unit=fleetmgr-trial-switch --setenv=PATH="$path" \
  "$new/bin/switch-to-configuration" test || s=$?
journalctl -u fleetmgr-trial-switch -o cat -n 15 --no-pager || true
if [ "$s" -ne 0 ]; then
  echo "the activation failed (code $s): going back to $old now" >&2
  systemctl stop fleetmgr-trial-rollback.timer || true
  systemd-run --quiet --wait --collect --unit=fleetmgr-trial-back --setenv=PATH="$path" \
    "$old/bin/switch-to-configuration" test || true
  exit 3
fi
echo "activated $new for a trial"
