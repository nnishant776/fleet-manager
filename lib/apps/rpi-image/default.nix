# rpi-image: build a generic Raspberry Pi 5 SD image on demand. A utility, not
# part of any host or rebuild: it extends the installer from nixos-raspberrypi
# with the first-boot step in ./firstboot.nix (assembled in ./image.nix). One image serves any number of
# cards and boards; per-card settings go on the card after flashing (see
# ./README.txt, which is also copied onto the card).
{ pkgs, self }:
pkgs.writeShellApplication {
  name = "rpi-image";
  runtimeInputs = [ pkgs.coreutils ];
  text = ''
    usage() {
      cat >&2 <<'USAGE'
    usage: rpi-image [-o FILE] [--flake REF]

      -o, --output FILE  where to write the compressed image
                         (default: ./nixos-rpi5.img.zst)
          --flake REF    flake to build from (default: the one running this)

    Builds a generic Raspberry Pi 5 image. Flash it to any number of cards,
    then add per-card settings in firstboot/ on each card's FAT partition.
    USAGE
      exit "''${1:-1}"
    }

    flake="path:${self}"
    output="./nixos-rpi5.img.zst"
    while [ $# -gt 0 ]; do
      case "$1" in
        -o|--output) output="''${2:?--output needs a value}"; shift 2 ;;
        --flake)     flake="''${2:?--flake needs a value}"; shift 2 ;;
        -h|--help)   usage 0 ;;
        *)           echo "rpi-image: unknown argument '$1'" >&2; usage ;;
      esac
    done

    export NIX_CONFIG="experimental-features = nix-command flakes"

    # The image is an aarch64-linux system; assembling it needs that platform,
    # natively, through emulation or on a remote builder.
    platforms=" $(nix config show system) $(nix config show extra-platforms) "
    case "$platforms" in
      *" aarch64-linux "*) ;;
      *)
        echo "rpi-image: this machine cannot build aarch64-linux. On a NixOS host, add" >&2
        echo "  boot.binfmt.emulatedSystems = [ \"aarch64-linux\" ];" >&2
        echo "to its configuration and switch, or configure an aarch64 remote builder." >&2
        exit 1 ;;
    esac

    # The vendor kernel and firmware come prebuilt from the nixos-raspberrypi
    # cache. Nix only honours an extra cache for trusted users; anyone else
    # builds the kernel themselves, which under emulation takes hours.
    cache=(
      --option extra-substituters "https://nixos-raspberrypi.cachix.org"
      --option extra-trusted-public-keys "nixos-raspberrypi.cachix.org-1:4iMO9LXa8BqhU+Rpg6LQKiGa2lsNh/j2oiYLNOQ5sPI="
    )
    if [ "$(id -u)" -ne 0 ] && ! nix config show trusted-users | tr ' ' '\n' | grep -qx "$(id -un)"; then
      echo "rpi-image: note: you are not a trusted Nix user, so the prebuilt kernel cache is" >&2
      echo "  ignored and the kernel will be compiled. Run with sudo to use the cache." >&2
    fi

    export RPI_IMAGE_FLAKE="$flake"
    # shellcheck disable=SC2016 # a Nix expression, not shell
    image="$(nix build --impure --no-link --print-out-paths "''${cache[@]}" --expr '
      let
        flake = builtins.getFlake (builtins.getEnv "RPI_IMAGE_FLAKE");
        image = import (flake.outPath + "/lib/apps/rpi-image/image.nix") { inherit (flake) inputs; };
      in image.config.system.build.sdImage')"

    cp "$image"/sd-image/*.img.zst "$output"
    chmod 644 "$output"
    echo "rpi-image: wrote $output"
    echo "Flash it with:"
    echo "  zstdcat $output | sudo dd of=/dev/sdX bs=4M status=progress conv=fsync"
    echo "then add hostname, authorized_keys and wifi files to firstboot/ on the card."
  '';
}
