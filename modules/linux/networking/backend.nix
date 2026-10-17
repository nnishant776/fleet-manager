{ config, lib, ... }:
let
  cfg = config.conf.networking;
  wifi = cfg.wireless.enable && cfg.wireless.wifi.enable;
in {
  # The `networkmanager` backend; the `networkd` one is in networkd.nix.
  config = lib.mkIf (cfg.enable && cfg.backend == "networkmanager") {
    networking.networkmanager.enable = lib.mkDefault true;

    # NetworkManager turns wpa_supplicant on by itself, at normal priority, so
    # Wi-Fi off has to be forced; without that every NetworkManager host has
    # Wi-Fi whatever conf says. It is also told to leave Wi-Fi devices alone.
    networking.wireless.enable = if wifi then lib.mkDefault true else lib.mkForce false;
    networking.networkmanager.unmanaged = lib.mkIf (!wifi) [ "type:wifi" ];
  };
}
