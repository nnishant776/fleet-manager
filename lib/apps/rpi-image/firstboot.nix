# Added to the Raspberry Pi installer image by the rpi-image app; not part of
# any host. Reads the files placed in firstboot/ on the card's FAT partition,
# once, on first boot.
{ config, lib, pkgs, ... }:
{
  # The installer fixes its hostname, which makes /etc/hostname read-only;
  # left unset, the first-boot service can give each card its own.
  networking.hostName = lib.mkForce "";

  systemd.services.rpi-firstboot = {
    description = "Apply per-card settings from the firmware partition";
    wantedBy = [ "multi-user.target" ];
    after = [ "NetworkManager.service" "systemd-hostnamed.service" ];
    before = [ "sshd.service" ];
    unitConfig = {
      RequiresMountsFor = "/boot/firmware";
      ConditionPathExists = "!/var/lib/rpi-firstboot.done";
    };
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = lib.getExe (import ./firstboot-script.nix { inherit pkgs; });
      ExecStartPost = "${pkgs.coreutils}/bin/touch /var/lib/rpi-firstboot.done";
    };
  };

  # A note on the card itself, for whoever edits it after flashing.
  sdImage.populateFirmwareCommands = lib.mkAfter ''
    mkdir -p firmware/firstboot
    cp ${./README.txt} firmware/firstboot/README.txt
  '';
}
