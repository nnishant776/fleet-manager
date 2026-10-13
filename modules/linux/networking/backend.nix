{ config, lib, ... }:
let
  cfg = config.conf.networking;
in {
  config = lib.mkIf cfg.enable {
    networking.networkmanager.enable = lib.mkDefault cfg.enable;
    networking.wireless.enable = lib.mkDefault (cfg.wireless.enable && cfg.wireless.wifi.enable);
  };
}
