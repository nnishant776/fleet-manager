{ lib, ... }: {
  imports = [
    # The radios share one master switch under `wireless`.
    (lib.mkRenamedOptionModule
      [ "conf" "networking" "wifi" "enable" ]
      [ "conf" "networking" "wireless" "wifi" "enable" ])
    (lib.mkRenamedOptionModule
      [ "conf" "hardware" "bluetooth" "enable" ]
      [ "conf" "networking" "wireless" "bluetooth" "enable" ])
  ];

  options.conf.networking = {
    enable = lib.mkEnableOption "networking (NetworkManager)";

    # Radios only: wired networking follows `enable` alone, whatever is set here.
    wireless = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Master switch for the radios: the Wi-Fi client and Bluetooth.
          `false` turns them all off whatever their own switches say; `true`
          leaves each to its own switch. Wired networking is not affected.
          Not the NixOS option of the same name, which is wpa_supplicant alone.
        '';
      };
      wifi.enable = lib.mkEnableOption "the Wi-Fi client";
      bluetooth.enable = lib.mkEnableOption "the Bluetooth stack";
    };

    firewall = {
      enable = lib.mkOption {
        type = lib.types.nullOr lib.types.bool;
        default = null;
        description = ''
          `null` leaves the NixOS firewall at its default, which is on. `true`
          or `false` sets it explicitly; `false` is rejected on a fleet host.
          Ports are opened by the service that owns them, never listed here.
        '';
      };
      config = lib.mkOption {
        type = lib.types.attrs;
        default = { };
        description = "Merged into `networking.firewall`.";
      };
    };
  };
}
