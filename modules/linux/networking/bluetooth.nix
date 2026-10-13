{ config, pkgs, lib, ... }:
let
  cfg = config.conf.networking;
in {
  config = lib.mkIf (cfg.enable && cfg.wireless.enable && cfg.wireless.bluetooth.enable) {
    environment.systemPackages = [ pkgs.bluez ];

    hardware = {
      enableAllFirmware = true;
      bluetooth.enable = true;
      alsa.enableBluetooth = true;
    };

    # Bluetooth management service
    services.blueman.enable = true;
  };
}
