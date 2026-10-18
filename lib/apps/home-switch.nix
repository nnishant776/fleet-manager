# Resolves the current user and host in the shell, so that the flake
# outputs stay pure. --impure is needed here, and only here, because a
# user's own ~/.config/home-manager/home.nix lives outside the flake.
{ pkgs }:
pkgs.writeShellApplication {
  name = "home-switch";
  runtimeInputs = [ ];
  text = ''
    user="''${1:-$(id -un)}"
    flakePath="''${2:-/etc/nixos}"
    host="''${3:-$(uname -n)}"

    if [ ! -e "$flakePath/flake.nix" ]; then
      echo "home-switch: no flake at $flakePath" >&2
      echo "usage: home-switch [username] [flake path] [host]" >&2
      exit 1
    fi

    target="$user@$host"

    # nix's messages are kept for a failure only, so a flake that cannot be
    # read (such as a checkout git does not trust) says why, while warnings
    # on success stay quiet.
    err="$(mktemp)"
    if ! names="$(nix eval --impure --json "$flakePath#homeConfigurations" \
                    --apply builtins.attrNames 2>"$err")"; then
      echo "home-switch: cannot evaluate the flake at $flakePath:" >&2
      cat "$err" >&2
      rm -f "$err"
      exit 1
    fi
    rm -f "$err"
    keys="$(printf '%s' "$names" | tr -d '[]"' | tr ',' '\n')"

    if ! printf '%s\n' "$keys" | grep -qxF "$target"; then
      # A system-managed home has no published configuration by design, so
      # say that rather than reporting a missing attribute.
      if nix eval "$flakePath#nixosConfigurations.$host.config.home-manager.users" \
           --apply "u: u ? \"$user\"" 2>/dev/null | grep -q true; then
        echo "home-switch: '$user' has an organisation-managed home on '$host'," >&2
        echo "  so there is nothing for you to activate — a system rebuild does it." >&2
        echo "  Personal configuration goes through review into users/$user/." >&2
        echo "  Self-management is granted per user with" >&2
        echo "  conf.users.accounts.<user>.selfManagedHome = true." >&2
        exit 1
      fi

      echo "home-switch: $flakePath declares no home configuration '$target'" >&2
      echo "available:" >&2
      # shellcheck disable=SC2086 # one key per line is the intent
      printf '  %s\n' $keys >&2
      exit 1
    fi

    exec nix run home-manager -- switch --flake "$flakePath#$target" --impure
  '';
}
