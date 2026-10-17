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
    mkHost = import ./lib/mkHost.nix { inherit inputs; };
    mkUser = import ./lib/mkUser.nix { inherit inputs; };

    hostNames = builtins.attrNames (
      lib.filterAttrs (_: t: t == "directory") (builtins.readDir ./hosts)
    );

    hostPlatforms = lib.genAttrs hostNames (h:
      let
        hostFile = import (./hosts + "/${h}/default.nix");
        raw = if builtins.isFunction hostFile then hostFile { config = {}; pkgs = {}; lib = lib; } else hostFile;
      in
        raw.nixpkgs.hostPlatform
    );

    linuxHosts = lib.filterAttrs (_: p: (lib.systems.elaborate p).isLinux) hostPlatforms;
    darwinHosts = lib.filterAttrs (_: p: (lib.systems.elaborate p).isDarwin) hostPlatforms;

    nixosConfigurations = lib.mapAttrs (h: _: mkHost { hostName = h; hostDir = ./hosts/${h}; }) linuxHosts;
    darwinConfigurations = lib.mapAttrs (h: _: mkHost { hostName = h; hostDir = ./hosts/${h}; }) darwinHosts;

    # The two sets are keyed by host directory and a directory has one platform,
    # so they cannot overlap; unionOfDisjoint says so and fails loudly if that
    # ever stops being true, where // would silently keep one side.
    allHosts = lib.attrsets.unionOfDisjoint nixosConfigurations darwinConfigurations;

    homeConfigurations = mkUser.mkHomeConfigurations {
      hostConfigs = allHosts;
      usersDir = ./users;
    };

    # One set of apps per system any host uses; the scripts live in lib/apps.
    systems = lib.unique (lib.attrValues (lib.attrsets.unionOfDisjoint linuxHosts darwinHosts));
    apps = lib.genAttrs systems (system:
      import ./lib/apps { inherit self; pkgs = nixpkgs.legacyPackages.${system}; });

    checks.x86_64-linux = {
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
    }
    # One `<host>-hotspot-ready` per host that has prepared a hotspot but not
    # turned it on, so turning it on stays a one-line change.
    // lib.mapAttrs'
      (name: host: lib.nameValuePair "${name}-hotspot-ready"
        (import ./tests/networking/hotspot-ready.nix {
          inherit name host;
          pkgs = nixpkgs.legacyPackages.x86_64-linux;
        }))
      (lib.filterAttrs
        (_: host: let hs = host.config.conf.networking.wireless.hotspot; in hs.adapter != null && !hs.enable)
        nixosConfigurations);
  in {
    inherit nixosConfigurations darwinConfigurations homeConfigurations apps checks;
  };
}
