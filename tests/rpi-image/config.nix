# Evaluates the real Raspberry Pi image, as `nix run .#rpi-image` builds it,
# without building anything for aarch64: instantiating the image runs the
# assertions in lib/apps/rpi-image/firstboot.nix, so `nix flake check` fails if
# an update of nixos-raspberrypi or nixos-images brings back a second network
# manager or a read-only hostname.
# Run it with `nix build .#checks.x86_64-linux.rpi-image-config`.
{ pkgs, inputs }:
let
  image = import ../../lib/apps/rpi-image/image.nix { inherit inputs; };
in
pkgs.writeText "rpi-image-config"
  (builtins.unsafeDiscardStringContext image.config.system.build.sdImage.drvPath)
