{ config, pkgs, lib, ... }:
let
  isLinux = pkgs.stdenv.isLinux;
  lixpkgs = pkgs.lixPackageSets;

  # Which kind of system this configuration is being deployed to. Declared by
  # conf.platform and set by lib/mkHost.nix, rather than sniffed from the
  # evaluating machine.
  isNixOS = config.conf.platform == "nixos";
in {
  nix = {
    enable = true;
    # Lix only where this configuration manages an externally installed nix
    # (nix-darwin, a foreign distro) — on NixOS, nix is already part of the closure.
    package = lib.mkIf (!isNixOS) lixpkgs.stable.lix;
    gc = {
      automatic = true;
      dates = lib.mkIf isLinux "weekly";
      options = "--delete-older-than 30d";
    };
    # Identical files in different store paths are hard-linked together on a
    # timer, so the many near-identical generations share their bytes.
    # auto-optimise-store would do it on every build instead, but nix-darwin
    # rejects it for the Nix and Lix versions known to corrupt the store.
    optimise.automatic = true;
    settings = {
      experimental-features = [ "nix-command" "flakes" ];
      # On a server, only administrators drive the daemon; a workstation keeps
      # the default so its user can build.
      allowed-users = lib.mkIf (config.conf.platform == "nixos" && config.conf.machineType == "headless") (lib.mkDefault [ "@wheel" ]);
    };
  };

  nixpkgs.config = {
    allowUnfree = true;
    allowUnfreePredicate = (_: true);
  };

  nixpkgs.overlays = [
    (final: prev: {
      inherit (prev.lixpkgs.stable)
        nixpkgs-review
        nix-eval-jobs
        nix-fast-build
        colmena;
    })
  ];
}
