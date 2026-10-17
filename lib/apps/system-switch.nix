# No --impure here: the system configurations evaluate purely, and
# passing it would hide a regression that `check-purity` exists to catch.
{ pkgs }:
pkgs.writeShellApplication {
  name = "system-switch";
  runtimeInputs = [ ];
  text = ''
    host="''${1:-$(uname -n)}"
    flakePath="''${2:-/etc/nixos}"
    os="$(uname)"

    # A flake reference (github:org/repo, path:/x) goes to Nix as it is; a
    # directory must hold a flake.
    case "$flakePath" in
      *:*) ;;
      *)
        if [ ! -e "$flakePath/flake.nix" ]; then
          echo "system-switch: no flake at $flakePath" >&2
          echo "usage: system-switch [hostname] [flake path or reference]" >&2
          exit 1
        fi ;;
    esac

    if [ "$os" = "Darwin" ]; then
      # The darwin-rebuild pinned by the flake, not the registry's nix-darwin.
      exec sudo nix run "$flakePath#darwin-rebuild" -- switch --flake "$flakePath#$host"
    else
      if grep -qi "nixos" /etc/os-release > /dev/null 2>&1; then
        exec sudo nixos-rebuild switch --flake "$flakePath#$host"
      else
        exec sudo nix run system-manager -- switch --flake "$flakePath#$host"
      fi
    fi
  '';
}
