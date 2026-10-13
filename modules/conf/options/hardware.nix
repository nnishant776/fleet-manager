{ lib, ... }: {
  options.conf.hardware = {
    graphics = {
      enable = lib.mkEnableOption "hardware graphics acceleration";
      vendor = lib.mkOption {
        type = lib.types.enum [ "intel" "amd" "nvidia" ];
        default = "intel";
        description = "GPU vendor, which selects the driver packages.";
      };
      extraPackages = lib.mkOption {
        type = lib.types.listOf lib.types.package;
        default = [ ];
        description = "Extra graphics packages.";
      };
      nix-ldLibraries = lib.mkOption {
        type = lib.types.listOf lib.types.package;
        default = [ ];
        description = "Runtime shared libraries exported to nix-ld for graphics.";
      };
    };

    powerManagement.enable = lib.mkEnableOption "the power management daemons (tuned, upower)";

    disk = {
      enable = lib.mkEnableOption "generating `disko.devices` from the options below; when off, the host provides its own `disko-config.nix` or is not installable with `os-install`";
      device = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "/dev/nvme0n1";
        description = ''
          Primary disk: boot partitions and the first physical volume.
          `os-install -d main=DEV` overrides it at install time. Required when
          `enable` is set; `null` otherwise, so an opted-out host still
          evaluates.
        '';
      };
      encrypt = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Wrap every physical volume in a LUKS2 container (`allowDiscards`). Turn off for VMs and machines that opt out.";
      };
      espSize = lib.mkOption {
        type = lib.types.str;
        default = "1G";
        description = "Size of the EFI system partition.";
      };
      volumes = lib.mkOption {
        type = lib.types.attrsOf (lib.types.submodule {
          options = {
            enable = lib.mkOption {
              type = lib.types.bool;
              default = true;
              description = "Whether this volume exists. `false` removes a default volume, for example `swap` on a VM.";
            };
            type = lib.mkOption {
              type = lib.types.enum [ "filesystem" "swap" ];
              default = "filesystem";
              description = "What the volume holds.";
            };
            size = lib.mkOption {
              type = lib.types.str;
              example = "16G";
              description = "Size in LVM syntax: `16G`, `20%VG`, `100%FREE`. Exactly one volume may use `100%FREE`.";
            };
            mountpoint = lib.mkOption {
              type = lib.types.nullOr lib.types.str;
              default = null;
              description = "Where the volume is mounted. Required for filesystems.";
            };
            fsType = lib.mkOption {
              type = lib.types.str;
              default = "ext4";
              description = "Filesystem to create; any that disko supports (`ext4`, `xfs`, `btrfs`, ...).";
            };
            mountOptions = lib.mkOption {
              type = lib.types.listOf lib.types.str;
              default = [ "defaults" ];
              description = "Mount options.";
            };
            neededForBoot = lib.mkOption {
              type = lib.types.bool;
              default = false;
              description = "Mount the volume in the initrd.";
            };
            priority = lib.mkOption {
              type = lib.types.int;
              default = 1000;
              description = "Creation order within the volume group; lower is created first.";
            };
          };
        });
        default = { };
        description = ''
          Logical volumes in the volume group `main`. Keys merge, so a host adds
          volumes without restating the defaults the disk module declares, and
          drops a default with `enable = false`.
        '';
      };
      extraDevices = lib.mkOption {
        type = lib.types.attrsOf lib.types.str;
        default = { };
        example = lib.literalExpression ''{ data = "/dev/sdb"; }'';
        description = ''
          Further whole disks that join the volume group as physical volumes,
          keyed by a short name. `os-install -d <name>=DEV` overrides each.
        '';
      };
    };

    boot = {
      mode = lib.mkOption {
        type = lib.types.enum [ "bios" "uefi" ];
        default = "uefi";
        description = "Firmware interface the machine boots with.";
      };
      loader = lib.mkOption {
        type = lib.types.nullOr (lib.types.enum [ "systemd-boot" "grub" "uboot" ]);
        default = "systemd-boot";
        description = "Bootloader to install. Defaults to GRUB when `mode` is `bios`.";
      };
      efiVariables = lib.mkEnableOption "letting the bootloader write EFI variables";
      tpm2Unlock = lib.mkEnableOption "unlocking LUKS volumes with the TPM at boot (systemd-cryptenroll; the passphrase remains as fallback)";
      grubPasswordHash = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "grub.pbkdf2.sha512.10000.674DFF...";
        description = ''
          GRUB superuser password hash, from `grub-mkpasswd-pbkdf2`. GRUB has no
          switch to turn off its menu editor, which lets anyone at the keyboard
          change the kernel command line (`init=/bin/sh`); a superuser does.
          The current system still boots without a password; editing an entry,
          the GRUB console and older generations ask for it. Only used when
          the loader is GRUB; the hash is stored in the open, which is what
          PBKDF2 hashes are for. It cannot come from `conf.secrets`, since the
          bootloader is installed before secrets are decrypted.
        '';
      };
      splash = {
        enable = lib.mkEnableOption "the boot splash (Plymouth)";
        theme = lib.mkOption {
          type = lib.types.str;
          default = "bgrt";
          description = "Plymouth theme. `bgrt` shows the firmware vendor logo.";
        };
        themePackages = lib.mkOption {
          type = lib.types.listOf lib.types.package;
          default = [ ];
          description = ''
            Packages providing `theme`. The themes bundled with Plymouth (bgrt,
            spinner, tribar, fade-in, glow, solar, spinfinity, script, details,
            text) need none.
          '';
        };
      };
    };
  };
}
