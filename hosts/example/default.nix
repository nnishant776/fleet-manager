# Reference host that shows every `conf.*` option, for looking up how an option
# is written; it is not meant to be deployed. Active lines are this host's
# values. Commented lines are options it leaves off or that do not fit it
# (Darwin, fleet, secrets, the disk layout, the networkd backend). It pulls in
# a lot of packages (every SDK, two desktop environments, container and VM
# stacks). Copy what you need; do not switch to it.
{ pkgs, lib, ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";
  fileSystems."/" = { device = "/dev/null"; fsType = "ext4"; };

  conf = {
    # What the machine is for. Roles apply `lib.mkDefault`, so every explicit
    # value in this file wins. (one of: minimal|server|workstation|developer|gaming|embedded)
    role = "developer";
    # What the machine physically is. Deduced from the role when unset.
    # (one of: laptop|desktop|headless|vm)
    machineType = "desktop";

    host = {
      # Must equal the name of this host's directory under `hosts/`.
      name = "example";
      timezone = "Asia/Kolkata";
      # Also sets the `LC_*` variables.
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
          # Whether the system creates and maintains the account; null decides
          # by platform.
          manageAccount = null;
          # Numeric user and primary group ids; null leaves them to the system
          # (the uid is required on Darwin when the account is managed).
          uid = null;
          gid = null;
          # Extra groups beyond those derived from `privileged`.
          groups = [ ];
          # SSH public keys accepted for logging in as this account.
          sshKeys = [ ];
          # SSH public keys that unlock sudo for this account instead of a password.
          sudoSshKeys = [ ];
          # What the host takes from users/<name>/<host>.yaml in the secrets
          # repository (only on a host that sets `secrets.repo.url`).
          secrets = {
            # The account's password hash.
            password = true;
            # A per-host SSH key, deployed to /run/secrets/users/<name>/ssh_key.
            sshKey = false;
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
        # mode: (one of: bios|uefi); loader defaults to GRUB for bios.
        mode = "uefi";
        # (one of: systemd-boot|grub|uboot)
        loader = "systemd-boot";
        # Let the bootloader write EFI variables.
        efiVariables = false;
        # Unlock LUKS volumes with the TPM at boot.
        tpm2Unlock = false;
        # GRUB superuser password hash, from `grub-mkpasswd-pbkdf2`.
        # grubPasswordHash = "grub.pbkdf2.sha512.10000.674DFF...";
        # Boot splash (Plymouth).
        splash = {
          enable = false;
          # `bgrt` shows the firmware vendor logo.
          theme = "bgrt";
          # Packages providing `theme`, for themes Plymouth does not bundle.
          themePackages = [ ];
        };
      };
      graphics = {
        enable = true;
        # vendor: (one of: intel|amd|nvidia)
        vendor = "intel";
        # `extraPackages` adds graphics packages; `nix-ldLibraries` are shared
        # libraries exported to nix-ld for them.
        extraPackages = [ ];
        nix-ldLibraries = [ ];
      };
      # Power management daemons (tuned, upower).
      powerManagement.enable = true;

      # Disk layout generated for disko. Left off here: this host has no disk.
      disk.enable = false;
      # disk = {
      #   enable = true;
      #   # Primary disk: boot partitions and the first physical volume.
      #   device = "/dev/nvme0n1";
      #   # LUKS2 around every physical volume; turn off for VMs.
      #   encrypt = true;
      #   espSize = "1G";
      #   # Further whole disks that join the volume group.
      #   extraDevices = { data = "/dev/sdb"; };
      #   # Logical volumes in the volume group `main`; keys merge with the defaults.
      #   volumes = {
      #     data = {
      #       # false removes a default volume, such as `swap` on a VM.
      #       enable = true;
      #       # (one of: filesystem|swap)
      #       type = "filesystem";
      #       # LVM syntax; exactly one volume may use `100%FREE`.
      #       size = "100G";
      #       # Required for filesystems.
      #       mountpoint = "/data";
      #       # Any filesystem disko supports.
      #       fsType = "ext4";
      #       mountOptions = [ "defaults" ];
      #       # Mount in the initrd.
      #       neededForBoot = false;
      #       # Creation order in the volume group; lower is created first.
      #       priority = 1000;
      #     };
      #   };
      # };
    };

    networking = {
      enable = true;
      # What manages the network. (one of: networkmanager|networkd)
      # `networkd` is for appliances: a desktop environment turns NetworkManager
      # on, which fails the build under `networkd`.
      backend = "networkmanager";
      # Name every physical interface after its bus and MAC, as in
      # `wlu001a2b3c4d5e` or `enp001a2b3c4d5e`, from the next boot.
      macNames = false;

      firewall = {
        # null leaves the NixOS firewall at its default, which is on.
        enable = null;
        # Merged into `networking.firewall`, for options such as logging.
        config = { };
        # Host-only nftables rules for new connections to this machine, and
        # for ones passing through it.
        inputRules = "";
        forwardRules = "";
        # Whole nftables tables of this host's own, for what the rules above cannot say.
        tables = { };
        # tables.example = {
        #   # (one of: ip|ip6|inet|arp|bridge|netdev)
        #   family = "inet";
        #   # The table's chains, sets and maps.
        #   content = ''
        #     chain input {
        #       type filter hook input priority 0; policy accept;
        #       ip saddr 192.168.1.0/24 tcp dport 22 accept
        #     }
        #   '';
        # };
      };

      # Radios. Each switch flips only its own radio.
      wireless = {
        # Master switch for the Wi-Fi client, the hotspot and Bluetooth.
        enable = true;
        bluetooth.enable = false;
        wifi = {
          enable = false;
          # Needs `backend = "networkd"`: the interfaces the Wi-Fi client uses,
          # by name; `{ }` uses every Wi-Fi interface.
          interfaces = { };
          # interfaces.wlu001a2b3c4d5e = {
          #   # Lowest metric carries internet traffic; null leaves networkd's 1024.
          #   routeMetric = 2048;
          # };
        };
        # Needs `backend = "networkd"`: two-letter country whose radio rules apply.
        # countryCode = "IN";
        # Needs `backend = "networkd"`: hotspots sharing this machine's
        # connection, one per adapter, keyed by the adapter's interface name.
        hotspots = { };
        # hotspots.wlu00c0ca9c8d7e = {
        #   enable = true;
        #   # Network name, 1 to 32 bytes; defaults to the host name.
        #   ssid = "pi-guest";
        #   # (one of: 2g|5g|6g)
        #   band = "5g";
        #   # null picks 6 on 2g, 36 on 5g and 37 on 6g.
        #   channel = 36;
        #   # MHz; "max" takes the widest the adapter can. (one of: "max"|20|40|80|160|320)
        #   width = "max";
        #   # Let "max" use channels shared with radar.
        #   radarChannels = false;
        #   # (one of: wpa3|wpa2-wpa3)
        #   security = "wpa3";
        #   # The hotspot's own address and subnet.
        #   address = "10.42.0.1/24";
        #   # Let devices on the hotspot reach this machine's own services.
        #   hostAccess = false;
        #   # File on the machine holding only the password (8 to 63 characters).
        #   passwordFile = "/var/lib/hotspot/wlu00c0ca9c8d7e";
        #   # Extra hostapd settings, added to or overriding the generated ones.
        #   settings = { max_num_sta = 16; };
        #   # Extra hostapd configuration lines, after `settings`.
        #   extraConfig = "ap_max_inactivity=600";
        # };
      };
    };

    # Services that expose this machine to the network.
    sharing = {
      enable = false;
      # Host a remote desktop (GNOME RDP).
      rdp.enable = false;
      ssh = {
        enable = false;
        # Merged into `services.openssh`.
        config = { };
      };
    };

    desktop = {
      enable = true;
      # `packages` replaces the curated desktop applications, `extraPackages`
      # appends to them.
      packages = [ ];
      extraPackages = [ ];
      # Seconds of inactivity before the session locks.
      idleLockSeconds = 600;
      # Print to network printers: CUPS on localhost, driverless.
      printing.enable = true;
      # Login screen background, scaled to cover the screen. The default is a
      # file in this repository.
      # greeter.wallpaper = ./wallpaper.png;

      # Enabling several makes them all available as session choices at the greeter.
      environments = {
        gnome.enable = true;
        hyprland = {
          enable = true;
          # shell: (one of: none|caelestia|noctalia|dms)
          shell = "dms";
        };
        sway.enable = false;
      };

      # System-level fonts; `packages` replaces the curated default set,
      # `extraPackages` appends to it.
      fonts = {
        packages = with pkgs; [
          nerd-fonts.jetbrains-mono
          nerd-fonts.fira-code
          nerd-fonts.iosevka
        ];
        extraPackages = [ ];
      };

      # Audio and video stack (codecs, players); `nix-ldLibraries` are
      # shared libraries exported to nix-ld for it.
      multimedia = {
        enable = true;
        extraPackages = [ ];
        nix-ldLibraries = [ ];
      };
    };

    # Container engines (Docker and Podman).
    containers = {
      enable = true;
      extraPackages = [ ];
      # Registries images may be pulled from.
      registries = [ "docker.io" "quay.io" "ghcr.io" "registry.k8s.io" ];
    };

    # Hypervisor (KVM, QEMU, libvirt, virt-manager).
    virtualisation = {
      enable = true;
      extraPackages = [ ];
      nix-ldLibraries = [ ];
    };

    flatpak.enable = true;

    # Darwin only; the rest is ignored on NixOS.
    homebrew = {
      enable = false;
      # brews = [ "jq" ];
      # casks = [ "firefox" ];
      # Mac App Store apps, as `Name = AppID`.
      # masApps = { Xcode = 497799835; };
      # onActivation = {
      #   autoUpdate = false;
      #   # What to do with formulae and casks not listed here. (one of: none|uninstall|zap)
      #   cleanup = "none";
      #   upgrade = false;
      # };
    };

    # Pull this configuration from a git remote and rebuild on a timer. Needs
    # `repo.url`, and `signing.allowedSignersFile` to check commit signatures.
    fleet = {
      # Where the configuration checkout lives on the machine.
      localPath = "/etc/nixos";
      # repo = {
      #   url = "https://github.com/org/fleet-manager.git";
      #   # Branch or tag to track.
      #   ref = "main";
      #   # Key in the host's secrets file (`hosts/<host>.yaml`) for a private
      #   # remote; needs `secrets.repo.url`.
      #   deployKeySecret = "deploy-key";
      # };
      # SSH allowed-signers file (`<principal> <key-type> <key>` per line).
      # signing.allowedSignersFile = ../../fleet/allowed_signers;
      autoUpdate = {
        # null turns it on when `repo.url` is set.
        enable = null;
        # systemd `OnCalendar` expression for the sync timer.
        dates = "04:00";
        # Random delay added to each run, so a fleet does not hit the cache at once.
        randomizedDelaySec = 1800;
        # Reboot after a switch when the kernel or initrd changed.
        allowReboot = false;
        # Hard-reset the checkout to the tracked ref before building.
        resetLocalChanges = true;
        # Return to the previous system if activating the new one fails.
        rollbackOnFailure = true;
      };
      # An account for the maintainer to reach this host with (needs
      # `sharing.ssh.enable = true`).
      # support = {
      #   enable = true;
      #   # Name of the support account.
      #   user = "support";
      #   # The maintainer's public SSH keys.
      #   sshKeys = [ "ssh-ed25519 AAAA... maintainer" ];
      #   # Also take a password hash for the account from `hosts/<host>.yaml`.
      #   consolePassword = false;
      # };
    };

    # Private repository this host fetches its secrets from (needs
    # `fleet.signing.allowedSignersFile` and `fleet.repo.url`). Accounts then take
    # their password from it, so drop `initialHashedPassword` or set
    # `secrets.password = false`.
    secrets = {
      # Where the host's identity, enrollment file and secrets snapshot live.
      statePath = "/var/lib/fleet";
      # repo = {
      #   url = "git@github.com:org/fleet-secrets.git";
      #   ref = "main";
      # };
    };

    development = {
      enable = true;
      # Extra top-level development packages.
      extraPackages = [ ];

      # Curated shared libraries exported to nix-ld for downloaded binaries.
      # `libraries` replaces the default set; `extraLibraries` appends.
      nixLd = {
        enable = true;
        libraries = with pkgs; [ libGL gtk3 ];
        extraLibraries = [ ];
      };

      # SDK language toolchains. In each group `packages` replaces the default
      # set, `extraPackages` appends to it, and `nix-ldLibraries` are shared
      # libraries exported to nix-ld.
      sdk = {
        base = {
          enable = true;
          packages = [ ];
          extraPackages = [ ];
          nix-ldLibraries = [ ];
        };
        cpp = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ clang-tools valgrind ];
          nix-ldLibraries = with pkgs; [ boost ];
        };
        nix = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ nil nixpkgs-fmt ];
          nix-ldLibraries = [ ];
        };
        lua = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ lua-language-server stylua ];
          nix-ldLibraries = [ ];
        };
        go = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ gopls golangci-lint ];
          nix-ldLibraries = [ ];
        };
        rust = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ rust-analyzer clippy ];
          nix-ldLibraries = [ ];
        };
        python = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ pyright black ];
          nix-ldLibraries = with pkgs; [ zlib ];
        };
        java = {
          enable = true;
          packages = [ ];
          extraPackages = with pkgs; [ jdt-language-server ];
          nix-ldLibraries = [ ];
        };
        cue = {
          enable = true;
          packages = [ ];
          extraPackages = [ ];
          nix-ldLibraries = [ ];
        };
      };

      # Editors. `configRepo` (a git remote) and `configPath` (a local path) are
      # for the editor's own configuration; no module reads them yet.
      editors = {
        neovim = {
          enable = true;
          configRepo = "https://github.com/example/nvim-config.git";
          configPath = "";
          packages = [ ];
          extraPackages = with pkgs; [ tree-sitter ripgrep fd ];
          nix-ldLibraries = [ ];
        };
        emacs = {
          enable = true;
          configRepo = "https://github.com/example/doom-emacs-config.git";
          configPath = "";
          packages = [ ];
          extraPackages = with pkgs; [ ripgrep fd ispell ];
          nix-ldLibraries = [ ];
        };
        vscode = {
          enable = true;
          configRepo = "";
          configPath = "";
          packages = [ ];
          extraPackages = [ ];
          nix-ldLibraries = [ ];
        };
      };

      tools = {
        gemini = {
          enable = true;
          packages = [ ];
          extraPackages = [ ];
          nix-ldLibraries = [ ];
        };
        opencode = {
          enable = true;
          packages = [ ];
          extraPackages = [ ];
          nix-ldLibraries = [ ];
        };
        rtk = {
          enable = true;
          packages = [ ];
          extraPackages = [ ];
          nix-ldLibraries = [ ];
        };
      };
    };
  };
}
