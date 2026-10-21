# Makes an existing git checkout in /etc/nixos the checkout of the
# configuration repository, before fleet host enroll switches the host to its
# fleet configuration, whose sync then owns it. Run as root, on the host:
#
#   sh -c "$(cat prepare-etc-nixos.sh)" sh URL REF [REV [DIR [STATE]]]
#
# URL and REF are conf.fleet.repo.url and .ref. REV is the commit the host is
# about to switch to, if it is known to be signed: when the repository has it,
# the checkout is left at it and it becomes the first commit the machine
# accepted, as the first-boot clone records an install's revision. DIR and
# STATE are /etc/nixos and /var/lib/fleet-sync unless a test says otherwise.
#
# What was there stays reachable: the old remote as before-fleet, the old
# commit on the branch before-fleet. A checkout with uncommitted changes or
# untracked files is refused, so that nothing is lost.
set -eu
url=$1 ref=$2 rev=${3:-} dir=${4:-/etc/nixos} state=${5:-/var/lib/fleet-sync}
g() { git -c safe.directory="$dir" -C "$dir" "$@"; }

if [ -n "$(g status --porcelain)" ]; then
  echo "$dir has uncommitted changes or untracked files: commit, move or remove them first" >&2
  exit 1
fi
if g remote get-url origin >/dev/null 2>&1; then
  g remote rename origin before-fleet
fi
if g rev-parse -q --verify HEAD >/dev/null; then
  g branch -f before-fleet HEAD
fi
g remote add origin "$url"
g fetch -q origin "+refs/heads/$ref:refs/remotes/origin/$ref"

record=""
if [ -n "$rev" ] && g merge-base --is-ancestor "$rev" "origin/$ref" 2>/dev/null; then
  g checkout -q -B "$ref" "$rev"
  record=$rev
else
  g checkout -q -B "$ref" "origin/$ref"
fi
g branch -q --set-upstream-to="origin/$ref" "$ref"
# Fleet machines pull; see modules/linux/fleet/sync.nix.
g remote set-url --push origin no_push
# The sync refuses a checkout anyone but root can write.
chown -R root:root "$dir"
chmod -R go-w "$dir"

if [ -n "$record" ]; then
  install -d -m 0755 "$state"
  printf '%s\n' "$record" > "$state/accepted"
  printf '%s\n' "$url" > "$state/accepted.url"
  chmod 0644 "$state/accepted" "$state/accepted.url"
  echo "$dir at $(g rev-parse --short HEAD), recorded as the first commit this machine accepted"
else
  echo "$dir at $(g rev-parse --short HEAD) of $ref"
fi
