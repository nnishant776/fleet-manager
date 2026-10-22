# Clones a git+ssh:// or git+https:// flake reference for an install, as
# os-install does for a private configuration:
#
#   fleet-clone-flake FLAKE-URL DIR
#
# ?ref= picks the branch. ?rev= pins the commit: the clone is left at exactly
# that commit, and a repository without it is an error (exit 3), not a reason
# to take its newest commit instead, since the commit was checked on the
# maintainer's machine and the newest one was not. GIT_SSH_COMMAND, with the
# host's key, comes from the caller. The secrets VM test runs it against its
# forge.
{ pkgs }:
pkgs.writeShellApplication {
  name = "fleet-clone-flake";
  runtimeInputs = [ pkgs.git pkgs.openssh ];
  text = ''
    if [ $# -ne 2 ]; then
      echo "usage: fleet-clone-flake FLAKE-URL DIR" >&2
      exit 2
    fi
    url="''${1#git+}" query="" ref="" rev=""
    case "$url" in *"?"*) query="''${url#*\?}"; url="''${url%%\?*}" ;; esac
    IFS='&' read -r -a params <<< "$query"
    for p in "''${params[@]}"; do
      case "$p" in
        ref=*) ref="''${p#ref=}" ;;
        rev=*) rev="''${p#rev=}" ;;
        *) echo "fleet-clone-flake: '$p' in $1 is not understood" >&2; exit 2 ;;
      esac
    done
    if [ -n "$rev" ] && ! [[ "$rev" =~ ^[0-9a-f]{40}$ ]]; then
      echo "fleet-clone-flake: rev=$rev is not a full commit hash" >&2
      exit 2
    fi

    args=(-q)
    if [ -n "$ref" ]; then args+=(--branch "$ref"); fi
    git clone "''${args[@]}" -- "$url" "$2" || exit 1
    if [ -n "$rev" ]; then
      if ! git -C "$2" cat-file -e "$rev^{commit}" 2>/dev/null; then
        echo "fleet-clone-flake: $url has no commit $rev: is it pushed?" >&2
        rm -rf "$2"
        exit 3
      fi
      git -C "$2" checkout -q --detach "$rev"
    fi
  '';
}
