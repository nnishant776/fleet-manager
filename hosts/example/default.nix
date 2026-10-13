# Reference host, for looking up how an option is written rather than for
# deployment. It sets a representative value for most of the `conf.*` tree, so
# it pulls in a lot of packages — every SDK, two desktop environments and both
# container and VM stacks. Copy from it; do not switch to it.
{ pkgs, lib, ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";
  fileSystems."/" = { device = "/dev/null"; fsType = "ext4"; };

  conf = {
    # What the machine is for. Roles apply `lib.mkDefault`, so every explicit
    # value in this file wins. (enum: minimal|server|workstation|developer|gaming|embedded)
    role = "developer";
    # What the machine physically is. Deduced from the role when unset.
    # (enum: laptop|desktop|headless|vm)
    machineType = "desktop";

    host = {
      name = "example";
      timezone = "Asia/Kolkata";
      locale = "en_IN";
    };

    users = {
      # Host-level switch for whether any home here is managed at all.
      manageHomes = true;

      # Accounts on this host, keyed by username: an administrative account and
      # an ordinary device user.
      accounts = {
        admin = {
          fullName = "Reference Admin";
          email = "admin@example.com";
          # Grants sudo, plus the privilege-adjacent groups for whichever
          # services this host enables.
          privileged = true;
          # mkpasswd format; a placeholder here.
          initialHashedPassword = "$y$j9T$Em3GOBdeSlR5rvnBakCQt1$MNH7/4KvTt423qqDDHsSUAz96SCUWm5AKMqjy5hzFS3";
          # Off, so a system rebuild activates this home and the user's own
          # ~/.config/home-manager is not read.
          selfManagedHome = false;
          # Extra Home Manager configuration for this user.
          homeConfig = {
            programs.starship.enable = true;
          };
        };

        bob = {
          fullName = "Bob Example";
          email = "bob@example.com";
          # No sudo, and network access limited to the narrow `network-users`
          # permissions rather than the full `networkmanager` group (with the
          # NetworkManager backend; under networkd they cannot change networks).
          privileged = false;
          homeConfig = {};
        };
      };
    };

    hardware = {
      boot = {
        # mode: (enum: bios|uefi); loader defaults to GRUB for bios.
        mode = "uefi";
        loader = "systemd-boot";
      };
      graphics = {
        enable = true;
        # vendor: (enum: intel|amd|nvidia)
        vendor = "intel";
      };
      # Power management daemons (tuned, upower).
      powerManagement.enable = true;
    };

    desktop = {
      enable = true;

      # Enabling several makes them all available as session choices at the greeter.
      environments = {
        gnome.enable = true;
        hyprland = {
          enable = true;
          # shell: (enum: none|caelestia|noctalia|dms)
          shell = "dms";
        };
        sway.enable = false;
      };

      # System-level fonts; replaces the curated default set.
      fonts.packages = with pkgs; [
        nerd-fonts.jetbrains-mono
        nerd-fonts.fira-code
        nerd-fonts.iosevka
      ];
    };

    # Container engines (Docker and Podman).
    containers.enable = true;
    # Hypervisor (KVM, QEMU, libvirt, virt-manager).
    virtualisation.enable = true;
    flatpak.enable = true;
    # Darwin only.
    homebrew.enable = false;

    development = {
      enable = true;

      # Curated shared libraries exported to nix-ld for downloaded binaries.
      # `libraries` replaces the default set; `extraLibraries` would append.
      nixLd = {
        enable = true;
        libraries = with pkgs; [ libGL gtk3 ];
      };

      # SDK language toolchains. `packages` replaces a group's default set,
      # `extraPackages` appends to it.
      sdk = {
        base.enable = true;
        cpp = {
          enable = true;
          extraPackages = with pkgs; [ clang-tools valgrind ];
          nix-ldLibraries = with pkgs; [ boost ];
        };
        nix = {
          enable = true;
          extraPackages = with pkgs; [ nil nixpkgs-fmt ];
        };
        lua = {
          enable = true;
          extraPackages = with pkgs; [ lua-language-server stylua ];
        };
        go = {
          enable = true;
          extraPackages = with pkgs; [ gopls golangci-lint ];
        };
        rust = {
          enable = true;
          extraPackages = with pkgs; [ rust-analyzer clippy ];
        };
        python = {
          enable = true;
          extraPackages = with pkgs; [ pyright black ];
          nix-ldLibraries = with pkgs; [ zlib ];
        };
        java = {
          enable = true;
          extraPackages = with pkgs; [ jdt-language-server ];
        };
        cue.enable = true;
      };

      editors = {
        neovim = {
          enable = true;
          configRepo = "https://github.com/example/nvim-config.git";
          extraPackages = with pkgs; [ tree-sitter ripgrep fd ];
        };
        emacs = {
          enable = true;
          configRepo = "https://github.com/example/doom-emacs-config.git";
          extraPackages = with pkgs; [ ripgrep fd ispell ];
        };
        vscode.enable = true;
      };

      tools = {
        gemini.enable = true;
        opencode.enable = true;
        rtk.enable = true;
      };
    };
  };
}
