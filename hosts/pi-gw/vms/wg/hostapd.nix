# The home Wi-Fi AP on a USB adapter passed through from the host, imported
# when homeWifi = "usb" (addresses.nix); everything the USB AP needs is here.
# Put it on a different band (or at least a non-overlapping channel) from the
# Pi's own Wi-Fi uplink.
#
# The passphrase comes from the host as a systemd credential:
#   install -m 0640 -o root -g microvm-wg /dev/stdin /var/lib/microvm-secrets/wg/wifi.password
{ pkgs, ... }:
let
  a = import ../addresses.nix;
  ap = a.wg.apUsb;
  passwordFile = "/run/credentials/@system/wifi-password";
in {
  microvm.devices = [{
    bus = "usb";
    path = "vendorid=0x${ap.vendor},productid=0x${ap.product}";
  }];
  microvm.credentialFiles.wifi-password = "/var/lib/microvm-secrets/wg/wifi.password";

  # A stable name for the adapter, whatever bus path it lands on.
  systemd.network.links."10-wlan-lan" = {
    matchConfig.Driver = ap.driver;
    linkConfig.Name = "wlan-lan";
  };
  # hostapd puts the AP into br-lan itself once it is in AP mode.
  systemd.network.networks."20-wlan-lan" = {
    matchConfig.Name = "wlan-lan";
    linkConfig.Unmanaged = true;
  };

  services.hostapd = {
    enable = true;
    radios.wlan-lan = {
      band = "2g";
      channel = 6;
      countryCode = "IN";
      networks.wlan-lan = {
        ssid = "home";
        authentication = {
          # WPA3 for devices that have it, WPA2 for the rest.
          mode = "wpa3-sae-transition";
          saePasswords = [{ inherit passwordFile; }];
          wpaPasswordFile = passwordFile;
        };
        settings.bridge = "br-lan";
      };
    };
  };

  hardware.wirelessRegulatoryDatabase = true;
  # Only the MediaTek firmware, not all of linux-firmware, in the store image.
  hardware.firmware = [
    (pkgs.runCommand "mediatek-firmware" { } ''
      mkdir -p $out/lib/firmware
      cp -r ${pkgs.linux-firmware}/lib/firmware/mediatek $out/lib/firmware/
    '')
  ];
}
