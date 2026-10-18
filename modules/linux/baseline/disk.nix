# The disk layout is generated from conf.hardware.disk when `enable` is set
# (an LVM volume group over one or more disks, each physical volume wrapped in
# a LUKS container when `encrypt` is on). A host can instead bring its own
# hand-written disko-config.nix; the unlock logic reads whichever layout the
# host ends up with. The laptop warning applies to generated layouts only: a
# hand-written one is a deliberate choice already.
#
# With tpm2Unlock, systemd-cryptsetup tries the TPM first and falls back to the
# passphrase; os-install does the enrollment once the system is installed.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.hardware.boot;
  disk = config.conf.hardware.disk;

  # Names of the LUKS containers the disko layout declares, read from the
  # layout rather than from boot.initrd.luks.devices itself, which would be
  # circular.
  luksNames = lib.concatMap
    (d: lib.concatMap
      (part: lib.optional ((part.content.type or "") == "luks") part.content.name)
      (lib.attrValues (d.content.partitions or { })))
    (lib.attrValues config.disko.devices.disk);

  enabledVolumes = lib.filterAttrs (_: v: v.enable) disk.volumes;

  # A physical volume is the LUKS container around the LVM member when the
  # host is encrypted, and the bare member otherwise.
  physicalVolume = name:
    let
      pv = {
        type = "lvm_pv";
        vg = "main";
      };
    in
    if disk.encrypt then {
      type = "luks";
      name = "crypt-${name}";
      settings.allowDiscards = true;
      content = pv;
    } else pv;

  # The volume that takes the remaining space has to be created last, so it
  # gets a high priority unless the host picked one (the option default of
  # 1000 is taken to mean "not picked").
  priorityOf = v:
    if v.size == "100%FREE" && v.priority == 1000 then 2000 else v.priority;

  # Volumes that `disk-extend --grow` may enlarge: filesystems only, with the
  # facts the script needs because it cannot read the configuration at run time.
  growable = lib.filterAttrs (_: v: v.type == "filesystem") enabledVolumes;

  growCases = lib.concatStringsSep "\n" (lib.mapAttrsToList
    (name: v: "    ${lib.escapeShellArg name}) fstype=${lib.escapeShellArg v.fsType}; mountpoint=${lib.escapeShellArg (toString v.mountpoint)} ;;")
    growable);

  diskExtend = pkgs.writeShellApplication {
    name = "disk-extend";
    runtimeInputs = with pkgs; [ gptfdisk cryptsetup lvm2 e2fsprogs xfsprogs util-linux coreutils ];
    text = ''
      usage() {
        echo "usage: disk-extend <name> <device> [--grow <volume>]" >&2
        exit 1
      }

      if [ "$#" -ne 2 ] && [ "$#" -ne 4 ]; then usage; fi
      name="$1"
      device="$2"
      grow=""
      if [ "$#" -eq 4 ]; then
        if [ "$3" != "--grow" ]; then usage; fi
        grow="$4"
      fi

      if [ "$(id -u)" -ne 0 ]; then
        echo "disk-extend: must be run as root" >&2
        exit 1
      fi

      case "$name" in
        *[!a-z0-9-]* | "" | -*)
          echo "disk-extend: <name> may only contain a-z, 0-9 and '-'" >&2
          exit 1
          ;;
      esac

      if [ ! -b "$device" ]; then
        echo "disk-extend: $device is not a block device" >&2
        exit 1
      fi

      # Refuse anything that already carries data: a signature, a partition
      # table or a filesystem on the disk or on one of its partitions.
      signatures="$(wipefs -n "$device")"
      found="$(lsblk -no FSTYPE,PTTYPE "$device")"
      if [ -n "$signatures" ] || [ -n "''${found//[[:space:]]/}" ]; then
        echo "disk-extend: $device already carries a filesystem, partition table or other signature:" >&2
        echo "$signatures" >&2
        echo "$found" >&2
        exit 1
      fi

      # Work out what --grow means before touching the disk. The filesystem
      # types come from the configuration this command was built from.
      fstype=""
      mountpoint=""
      if [ -n "$grow" ]; then
        case "$grow" in
      ${growCases}
          *)
            echo "disk-extend: no growable filesystem volume named '$grow'" >&2
            exit 1
            ;;
        esac
        case "$fstype" in
          ext4 | xfs) ;;
          *)
            echo "disk-extend: cannot grow a $fstype filesystem; extend /dev/main/$grow by hand" >&2
            exit 1
            ;;
        esac
      fi

      sgdisk -Z "$device"
      sgdisk -n 1:0:0 -t 1:8E00 -c "1:disk-$name-main" "$device"
      blockdev --rereadpt "$device"

      case "$device" in
        *[0-9]) part="''${device}p1" ;;
        *) part="''${device}1" ;;
      esac
      for _ in $(seq 1 50); do
        if [ -b "$part" ]; then break; fi
        sleep 0.2
      done
      if [ ! -b "$part" ]; then
        echo "disk-extend: partition $part did not appear" >&2
        exit 1
      fi

      ${if disk.encrypt then ''
      echo "Choose the passphrase for the new container; use the same one as for the existing disks." >&2
      cryptsetup luksFormat --type luks2 "$part"
      cryptsetup open --allow-discards "$part" "crypt-$name"
      pv="/dev/mapper/crypt-$name"
      '' else ''
      pv="$part"
      ''}
      pvcreate "$pv"
      vgextend main "$pv"

      if [ -n "$grow" ]; then
        lvextend -l +100%FREE "/dev/main/$grow"
        case "$fstype" in
          ext4) resize2fs "/dev/main/$grow" ;;
          xfs) xfs_growfs "$mountpoint" ;;
        esac
      fi

      echo
      echo "Done. Add this to the host before the next switch:"
      echo
      echo "  conf.hardware.disk.extraDevices.$name = \"$device\";"
      echo
      echo "Switching without it leaves the layout out of step with the machine."
    '';
  };
in {
  config = lib.mkMerge [
    (lib.mkIf (cfg.tpm2Unlock && luksNames != [ ]) {
      boot.initrd.systemd.tpm2.enable = true;
      boot.initrd.luks.devices = lib.genAttrs luksNames (_: {
        crypttabExtraOpts = [ "tpm2-device=auto" ];
      });
    })

    # Defaults for the volume group. Every field is a default so that a host
    # overrides a single one (`volumes.swap.size = "4G"`) without restating
    # the rest.
    (lib.mkIf disk.enable {
      conf.hardware.disk.volumes = {
        fleet = {
          size = lib.mkDefault "64M";
          type = lib.mkDefault "filesystem";
          fsType = lib.mkDefault "ext4";
          mountpoint = lib.mkDefault "/var/lib/fleet";
          neededForBoot = lib.mkDefault true;
          priority = lib.mkDefault 100;
        };
        swap = {
          size = lib.mkDefault "16G";
          type = lib.mkDefault "swap";
        };
        root = {
          size = lib.mkDefault "100%FREE";
          type = lib.mkDefault "filesystem";
          fsType = lib.mkDefault "ext4";
          mountpoint = lib.mkDefault "/";
        };
      };

      disko.devices = {
        disk = {
          main = {
            device = disk.device;
            type = "disk";
            content = {
              type = "gpt";
              partitions = {
                bios_boot = lib.mkIf (config.conf.hardware.boot.mode == "bios") {
                  type = "EF02";
                  size = "1M";
                };
                boot = lib.mkIf (config.conf.hardware.boot.mode == "uefi") {
                  size = disk.espSize;
                  type = "EF00";
                  # U-Boot looks for extlinux.conf on partitions marked
                  # legacy BIOS bootable (GPT attribute 2).
                  attributes = lib.optionals (config.conf.hardware.boot.loader == "uboot") [ 2 ];
                  content = {
                    type = "filesystem";
                    format = "vfat";
                    mountpoint = "/boot";
                    mountOptions = [ "fmask=0077" "dmask=0077" ];
                  };
                };
                main = {
                  size = "100%";
                  content = physicalVolume "main";
                };
              };
            };
          };
        } // lib.mapAttrs
          (name: device: {
            inherit device;
            type = "disk";
            content = {
              type = "gpt";
              partitions.main = {
                size = "100%";
                content = physicalVolume name;
              };
            };
          })
          disk.extraDevices;

        lvm_vg.main = {
          type = "lvm_vg";
          lvs = lib.mapAttrs
            (_: v: {
              inherit (v) size;
              priority = priorityOf v;
              content =
                if v.type == "swap" then {
                  type = "swap";
                  # As the hand-written layouts had it: TRIM on swap, and the
                  # volume registered as the hibernation resume device.
                  discardPolicy = "both";
                  resumeDevice = true;
                } else {
                  type = "filesystem";
                  format = v.fsType;
                  inherit (v) mountpoint mountOptions;
                };
            })
            enabledVolumes;
        };
      };

      # sops-nix decrypts during activation, before most filesystems are
      # mounted, so the volumes holding its inputs must be up in the initrd.
      fileSystems = lib.mapAttrs'
        (_: v: lib.nameValuePair v.mountpoint { neededForBoot = true; })
        (lib.filterAttrs (_: v: v.neededForBoot && v.mountpoint != null) enabledVolumes);

      environment.systemPackages = [ diskExtend ];

      assertions = [
        {
          assertion = disk.device != null;
          message = "conf.hardware.disk.enable is set on ${config.conf.host.name} but conf.hardware.disk.device is not.";
        }
        {
          # The primary disk is `main` in disko.devices.disk; a second one of
          # that name would silently replace it.
          assertion = !(disk.extraDevices ? main);
          message = "conf.hardware.disk.extraDevices on ${config.conf.host.name} uses the name `main`, which is the primary disk; pick another name.";
        }
        {
          assertion = lib.length (lib.filter (v: v.size == "100%FREE") (lib.attrValues enabledVolumes)) <= 1;
          message = "conf.hardware.disk.volumes: at most one enabled volume may use size \"100%FREE\" (${config.conf.host.name}).";
        }
        {
          assertion = (disk.volumes.fleet.enable or false)
            && disk.volumes.fleet.mountpoint == "/var/lib/fleet"
            && disk.volumes.fleet.neededForBoot;
          message = "conf.hardware.disk.volumes.fleet must stay enabled, mounted at /var/lib/fleet and neededForBoot (${config.conf.host.name}); the host identity lives there.";
        }
        {
          assertion = lib.all (v: v.type != "filesystem" || v.mountpoint != null) (lib.attrValues enabledVolumes);
          message = "conf.hardware.disk.volumes: every enabled filesystem volume needs a mountpoint (${config.conf.host.name}).";
        }
      ];
    })

    # A warning, not an error: an unencrypted laptop is allowed, but it should
    # be a visible choice. Hand-written layouts are not judged here.
    {
      warnings = lib.optional (disk.enable && !disk.encrypt && config.conf.machineType == "laptop")
        "${config.conf.host.name} is a laptop and conf.hardware.disk.encrypt is off: the disk is unencrypted.";
    }
  ];
}
