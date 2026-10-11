{ lib, pkgs, ... }:
{
  config = {
    environment.sessionVariables = {
      PATH = [ "/usr/local/bin" "/usr/bin" "/opt/bin" ];
    };

    programs.nix-index.enable = true;

    # Hardware utilitites. Some are x86-only (biosdevname), so the list is
    # filtered to what the host's platform has, for the aarch64 hosts.
    environment.systemPackages = builtins.filter
      (lib.meta.availableOn pkgs.stdenv.hostPlatform)
      (with pkgs; [
        usbutils
        pciutils
        dmidecode
        biosdevname
      ]);
  };
}
