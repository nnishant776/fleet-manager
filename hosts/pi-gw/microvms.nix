# The VMs, declared from vms/ and rebuilt with the host. Each boots from its
# own store image (storeOnDisk) holding only its closure: the host's /nix/store
# is not shared, so a VM sees none of the host's or the other VM's store paths
# and no virtiofsd runs on the host. Each VM's QEMU runs as its own host user,
# microvm-<vm> (vm-users.nix).
#
# Secrets reach a VM as systemd credentials (microvm.credentialFiles), read by
# QEMU as that user from /var/lib/microvm-secrets/<vm>/, which is root's and
# readable by the VM's group only:
#   install -m 0640 -o root -g microvm-<vm> <file> /var/lib/microvm-secrets/<vm>/<name>
{ inputs, lib, ... }:
let
  a = import ./vms/addresses.nix;
  ap = a.wg.apUsb;
  usbAp = a.wg.homeWifi == "usb";
in {
  assertions = [{
    assertion = lib.elem a.wg.homeWifi [ "usb" "access-point" ];
    message = ''vms/addresses.nix: wg.homeWifi must be "usb" or "access-point", not "${a.wg.homeWifi}"'';
  }];

  imports = [
    inputs.microvm.nixosModules.host
    ./vm-users.nix
  ];

  microvm.vms = {
    public.config.imports = [ ./vms/public ];
    wg.config.imports = [ ./vms/wg ];
  };

  # With homeWifi = "usb", the home AP's USB Wi-Fi adapter belongs to the wg
  # VM: the host never binds a driver to it, and only the wg VM's QEMU may
  # open it.
  boot.blacklistedKernelModules = lib.mkIf usbAp [ ap.driver ];
  services.udev.extraRules = lib.mkIf usbAp ''
    SUBSYSTEM=="usb", ATTR{idVendor}=="${ap.vendor}", ATTR{idProduct}=="${ap.product}", GROUP="microvm-wg", MODE="0660"
  '';
}
