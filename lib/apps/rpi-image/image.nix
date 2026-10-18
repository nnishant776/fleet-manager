# The image the rpi-image app builds: the Raspberry Pi 5 installer from
# nixos-raspberrypi with the first-boot step added. Shared by the app and the
# rpi-image checks, so they see the same system.
{ inputs }:
inputs.nixos-raspberrypi.nixosConfigurations.rpi5-installer.extendModules {
  modules = [
    ./firstboot.nix
    # A note on the card itself, for whoever edits it after flashing.
    ({ lib, ... }: {
      sdImage.populateFirmwareCommands = lib.mkAfter ''
        mkdir -p firmware/firstboot
        cp ${./README.txt} firmware/firstboot/README.txt
      '';
    })
    # fleet, for the steps after the first boot (tools/fleet).
    ({ pkgs, ... }: {
      environment.systemPackages = [ (pkgs.callPackage ../../../tools/fleet/package.nix { }) ];
    })
  ];
}
