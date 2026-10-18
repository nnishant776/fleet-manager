{ config, lib, ... }:
let
  hwdCfg = config.conf.hardware;

  # The NixOS loader each conf.hardware.boot.loader value turns on; null turns
  # on none, for a board module that brings its own. U-Boot boots NixOS from
  # the /boot/extlinux/extlinux.conf that generic-extlinux-compatible writes.
  loaderModule =
    if hwdCfg.boot.loader == null then null
    else {
      systemd-boot = "systemd-boot";
      grub = "grub";
      uboot = "generic-extlinux-compatible";
    }.${hwdCfg.boot.loader};

  earlyKms = {
    intel = [ "i915" ];
    amd = [ "amdgpu" ];
  };
in {
  config = lib.mkMerge [
    {
      boot.loader.${loaderModule}.enable = true;
      boot.loader.efi.canTouchEfiVariables = hwdCfg.boot.efiVariables;
    }

    # A separate set: Nix does not allow the dynamic `${loader}` path above and a
    # static `systemd-boot` path in one literal.
    {
      # No editing the kernel command line from the boot menu (init=/bin/sh is
      # a root shell), and a bounded menu so /boot does not fill and old
      # generations do not stay bootable forever.
      boot.loader.systemd-boot.editor = lib.mkDefault false;
      boot.loader.systemd-boot.configurationLimit = lib.mkDefault 10;
      boot.loader.grub.configurationLimit = lib.mkDefault 10;
      boot.loader.generic-extlinux-compatible.configurationLimit = lib.mkDefault 10;
    }

    (lib.mkIf (hwdCfg.boot.mode == "bios") {
      conf.hardware.boot.loader = lib.mkDefault "grub";
    })

    # U-Boot must already be on the board (its flash, or the card's boot
    # area); this only writes the menu it reads. NixOS turns GRUB on by
    # default, and generic-extlinux-compatible, unlike systemd-boot, does not
    # turn it off.
    (lib.mkIf (hwdCfg.boot.loader == "uboot") {
      boot.loader.grub.enable = lib.mkDefault false;
      assertions = [
        {
          assertion = hwdCfg.boot.mode != "bios";
          message = "conf.hardware.boot.loader = \"uboot\" on '${config.conf.host.name}' needs"
            + " conf.hardware.boot.mode = \"uefi\" (the default): that gives conf.hardware.disk"
            + " the FAT /boot partition U-Boot reads extlinux.conf from, and \"bios\" has none.";
        }
        {
          assertion = !hwdCfg.boot.efiVariables;
          message = "conf.hardware.boot.efiVariables on '${config.conf.host.name}' has nothing to"
            + " write with loader = \"uboot\", which boots from extlinux.conf, not an EFI entry.";
        }
      ];
    })

    # GRUB's counterpart to systemd-boot's editor = false. GRUB cannot turn its
    # editor off, only put it behind a superuser; NixOS marks the current
    # entries --unrestricted, so booting stays password-free while editing,
    # the console and older generations need the password.
    (lib.mkIf (hwdCfg.boot.loader == "grub") {
      boot.loader.grub.users = lib.mkIf (hwdCfg.boot.grubPasswordHash != null) {
        root.hashedPassword = hwdCfg.boot.grubPasswordHash;
      };
      warnings = lib.optional (hwdCfg.boot.grubPasswordHash == null)
        ("conf.hardware.boot on '${config.conf.host.name}': GRUB's menu editor is open,"
          + " so anyone at the keyboard can edit the kernel command line (init=/bin/sh)."
          + " Set conf.hardware.boot.grubPasswordHash from grub-mkpasswd-pbkdf2.");
    })

    (lib.mkIf (hwdCfg.boot.splash.enable) {
      boot = {
        plymouth = {
          enable = true;
          theme = hwdCfg.boot.splash.theme;
          themePackages = hwdCfg.boot.splash.themePackages;
        };

        # Nothing but the splash on screen: kernel messages below "error",
        # udev and systemd status lines off (failures are still in the
        # journal), and no initrd chatter. systemd stage 1, on for every host,
        # is what lets Plymouth show the LUKS passphrase prompt on an
        # encrypted disk.
        consoleLogLevel = 3;
        kernelParams = [
          "quiet"
          "rd.udev.log_level=3"
          "udev.log_priority=3"
          "rd.systemd.show_status=false"
          "systemd.show_status=false"
        ];
        initrd.verbose = false;

        # The generation menu is hidden; it is still one key away. On
        # systemd-boot, press Space (or any key) repeatedly as the machine
        # starts. On GRUB, hold Shift or press Esc during the second it waits.
        # On U-Boot a timeout of 0 turns the menu off and boots the newest
        # generation. A host that wants the menu back sets boot.loader.timeout.
        loader.timeout = lib.mkDefault (if hwdCfg.boot.loader == "grub" then 1 else 0);
        loader.grub.timeoutStyle = lib.mkDefault "hidden";
      };
    })

    # Plymouth starts in the initrd, on the firmware's framebuffer with the
    # vendor logo. If the GPU driver only loads in stage 2, it takes the screen
    # over mid-boot: the logo is wiped and the console shows through. Loading
    # it in the initrd (early KMS) gives Plymouth the final display from the
    # start, and on recent Intel GPUs the driver keeps the firmware's mode, so
    # the logo never flickers. The same applies with or without disk
    # encryption. NVIDIA's proprietary driver is left out: in the initrd it is
    # large and fragile, and the firmware framebuffer serves well enough.
    (lib.mkIf (hwdCfg.boot.splash.enable && hwdCfg.graphics.enable) {
      boot.initrd.kernelModules = earlyKms.${hwdCfg.graphics.vendor} or [ ];
    })
  ];
}
