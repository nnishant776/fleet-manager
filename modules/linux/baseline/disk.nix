# Disk encryption follows from the host's disko layout (templates/disko-luks.nix
# is the starting point); this file only adds how the volumes are unlocked.
# With tpm2Unlock, systemd-cryptsetup tries the TPM first and falls back to the
# passphrase; os-install does the enrollment once the system is installed.
{ config, lib, ... }:
let
  cfg = config.conf.hardware.boot;

  # Names of the LUKS containers the disko layout declares, read from the
  # layout rather than from boot.initrd.luks.devices itself, which would be
  # circular.
  luksNames = lib.concatMap
    (disk: lib.concatMap
      (part: lib.optional ((part.content.type or "") == "luks") part.content.name)
      (lib.attrValues (disk.content.partitions or { })))
    (lib.attrValues config.disko.devices.disk);
in {
  config = lib.mkIf (cfg.tpm2Unlock && luksNames != [ ]) {
    boot.initrd.systemd.tpm2.enable = true;
    boot.initrd.luks.devices = lib.genAttrs luksNames (_: {
      crypttabExtraOpts = [ "tpm2-device=auto" ];
    });
  };
}
