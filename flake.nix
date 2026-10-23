{
  description = "Multi-platform NixOS / Darwin configuration flake";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-26.05";
    nixpkgs-unstable.url = "github:nixos/nixpkgs?ref=nixos-unstable";

    nix-darwin = {
      url = "github:nix-darwin/nix-darwin/nix-darwin-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    apple-fonts.url = "github:Lyndeno/apple-fonts.nix";

    home-manager = {
      url = "github:nix-community/home-manager/release-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    disko = {
      url = "github:nix-community/disko";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    sops-nix = {
      url = "github:Mic92/sops-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # Raspberry Pi board support, for the rpi-image app and the Pi hosts
    # (hosts/pi-gw). Deliberately not following this flake's nixpkgs, so its
    # vendor kernel matches its binary cache; fetched only when one of them is
    # evaluated.
    nixos-raspberrypi.url = "github:nvmd/nixos-raspberrypi";

    # Declarative VMs (microvm.nix); the host module is imported by hosts/pi-gw/guests.nix.
    microvm = {
      url = "github:microvm-nix/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    caelestia-shell = {
      url = "github:caelestia-dots/shell";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    noctalia = {
      url = "github:noctalia-dev/noctalia";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    dms = {
      url = "github:AvengeMedia/DankMaterialShell/stable";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = inputs@{ self, nixpkgs, ... }:
  let
    lib = nixpkgs.lib;
    # The library a private fleet builds on: lib.mkFleet (lib/mkFleet.nix).
    mkFleet = import ./lib/mkFleet.nix { inherit inputs; };

    # This repository's own fleet, the example and test hosts under hosts/,
    # built exactly as a private fleet's hosts are.
    ownFleet = mkFleet {
      inherit self;
      hosts = ./hosts;
      users = ./users;
      libraryApps = true;
    };
    inherit (ownFleet) nixosConfigurations darwinConfigurations homeConfigurations apps;

    # One set of packages per system any host uses, as for the apps.
    systems = builtins.attrNames apps;

    # fleet, the guided tool (tools/fleet): `fleet` brings the tools it runs,
    # `fleet-static` is the bare static binary for machines without Nix.
    packages = lib.genAttrs systems (system:
      let fleet = nixpkgs.legacyPackages.${system}.callPackage ./tools/fleet/package.nix { };
      in { inherit fleet; fleet-static = fleet.static; default = fleet; });

    checks.x86_64-linux = {
      # Builds fleet, which runs its unit tests.
      fleet = self.packages.x86_64-linux.fleet;
      # The fleet-secrets port against the shell tool, command by command.
      fleet-secrets-parity = import ./tests/fleet/secrets-parity.nix {
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
        fleet = self.packages.x86_64-linux.fleet;
      };
      inventory-publish = import ./tests/fleet/publish.nix {
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
        fleet = self.packages.x86_64-linux.fleet;
      };
      secrets-sync = import ./tests/secrets/vm-test.nix {
        inherit inputs self;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      pi-gw-vm-users = import ./tests/pi-gw/vm-users.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      rpi-image-config = import ./tests/rpi-image/config.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      rpi-image-firstboot = import ./tests/rpi-image/firstboot.nix { inherit inputs; };
      networking-networkd = import ./tests/networking/networkd.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      networking-hotspot = import ./tests/networking/hotspot.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      networking-forwarding = import ./tests/networking/forwarding.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      networking-names = import ./tests/networking/names.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      users-ssh-key-sudo = import ./tests/users/ssh-key-sudo.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      fleet-safe-directory = import ./tests/fleet/safe-directory.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      boot-uboot = import ./tests/boot/uboot.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      secrets-user-owner-only = import ./tests/secrets/user-secrets.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      fleet-enroll = import ./tests/fleet/enroll.nix {
        inherit inputs self;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      fleet-sync-status = import ./tests/fleet/sync-status.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
      # The journal, audit records included, sent to Loki.
      fleet-logs = import ./tests/fleet/logs.nix {
        inherit inputs;
        pkgs = nixpkgs.legacyPackages.x86_64-linux;
      };
    }
    # One `<host>-hotspot-ready` per host that has declared a hotspot but not
    # turned it on, so turning it on stays a one-line change.
    // lib.mapAttrs'
      (name: host: lib.nameValuePair "${name}-hotspot-ready"
        (import ./tests/networking/hotspot-ready.nix {
          inherit name host;
          pkgs = nixpkgs.legacyPackages.x86_64-linux;
        }))
      (lib.filterAttrs
        (_: host: lib.any (h: !h.enable) (lib.attrValues host.config.conf.networking.wireless.hotspots))
        nixosConfigurations);
  in {
    inherit nixosConfigurations darwinConfigurations homeConfigurations apps packages checks;
    lib = { inherit mkFleet; };
  };
}
