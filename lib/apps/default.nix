# The flake's apps, one file each. Every script is a writeShellApplication, so
# shellcheck runs when it is built and its tools come from runtimeInputs.
{ pkgs, self }:
let
  app = drv: { type = "app"; program = pkgs.lib.getExe drv; };
in {
  os-install    = app (import ./os-install.nix { inherit pkgs self; });
  system-switch = app (import ./system-switch.nix { inherit pkgs; });
  home-switch   = app (import ./home-switch.nix { inherit pkgs; });
  check-purity  = app (import ./check-purity.nix { inherit pkgs; });
  fleet-secrets = app (import ./fleet-secrets.nix { inherit pkgs; });
  # The guided tool; tools/fleet/package.nix.
  fleet         = app (pkgs.callPackage ../../tools/fleet/package.nix { });
} // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
  # On demand only: builds a Raspberry Pi SD image, never part of a rebuild.
  rpi-image = app (import ./rpi-image { inherit pkgs self; });

  # Local secrets repository and sshd for testing the fleet secrets design.
  secrets-harness = app (import ../../tests/secrets/harness.nix { inherit pkgs; });
} // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isDarwin {
  # nix-darwin's own tool from the input this flake pins, so the first switch
  # on a Mac uses the same nix-darwin as the modules it activates.
  darwin-rebuild = app self.inputs.nix-darwin.packages.${pkgs.stdenv.hostPlatform.system}.darwin-rebuild;
}
