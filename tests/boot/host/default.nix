# The machine in tests/boot/uboot.nix: a host that boots through U-Boot,
# built from this flake's own modules, with the disk layout so its /boot
# partition can be checked. Not under hosts/, so it is never a flake output.
{ ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "minimal";
    host.name = "ubootbox";

    hardware = {
      boot.loader = "uboot";
      disk = {
        enable = true;
        device = "/dev/vda";
      };
    };
  };
}
