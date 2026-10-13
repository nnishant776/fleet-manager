{ config, pkgs,lib, ... }:
let
  cfg = config.conf.networking;
  bluetooth = cfg.wireless.enable && cfg.wireless.bluetooth.enable;
in {
  config  = lib.mkIf cfg.enable {
    # Force bluez installation
    environment.systemPackages = with pkgs; [
      bluez
    ];

    # Enable bluetooth adapter
    hardware = lib.mkIf bluetooth {
      enableAllFirmware = true;
      bluetooth = {
        enable = true;
      };
      alsa = {
        enableBluetooth = true;
      };
    };

    # Enabe bluetooth management service
    services = lib.mkIf bluetooth {
      blueman.enable = true;
    };
  };
}
