# Makes /etc/nixos the configuration repository's checkout, after fleet host
# enroll has switched the host to its fleet configuration. Everything /etc/nixos
# held before is deleted: the checkout belongs to the framework. Run as root,
# on the host:
#
#   sh remake-etc-nixos.sh CONFIG_URL WAS_GIT [DIR [STATE]]
#
# fleet host enroll runs it as a unit (systemd-run), so a dropped connection
# cannot stop it between deleting /etc/nixos and starting the clone.
#
# CONFIG_URL is conf.fleet.repo.url. WAS_GIT is true when /etc/nixos was a git
# checkout before the switch, whatever it was a checkout of, and false
# otherwise. DIR and STATE are /etc/nixos and /var/lib/fleet-sync unless a
# test says otherwise.
#
# The switch's activation starts fleet-bootstrap, the first-boot clone, which
# replaces an /etc/nixos that has no checkout and records the commit it checks
# out in STATE/accepted and accepted.url. Starting it waits for that run to
# end, and does nothing when a checkout exists. A checkout that was there
# before, or one of something else, is deleted with that record, and the clone
# started again.
set -eu
url=$1 was_git=$2 dir=${3:-/etc/nixos} state=${4:-/var/lib/fleet-sync}

# Nothing is deleted on a host that could not make the checkout again.
if ! systemctl cat fleet-bootstrap >/dev/null 2>&1; then
  echo "this host has no fleet-bootstrap unit to make the checkout with, so $dir is left as it is" >&2
  exit 1
fi
g() { git -c safe.directory="$dir" -C "$dir" "$@"; }
ours() { [ -d "$dir/.git" ] && [ "$(g remote get-url origin 2>/dev/null)" = "$url" ]; }
clone() {
  systemctl start fleet-bootstrap || echo "fleet-bootstrap did not finish" >&2
}

clone
if [ "$was_git" = true ] || ! ours; then
  rm -rf "$dir"
  rm -f "$state/accepted" "$state/accepted.url"
  clone
fi

if ! ours; then
  echo "$dir is not a checkout of $url yet: the clone failed (journalctl -u fleet-bootstrap shows why)." >&2
  echo "The unit keeps trying every 60 seconds by itself. The host keeps running its configuration, and its sync waits for the checkout." >&2
  exit 1
fi
g rev-parse HEAD
