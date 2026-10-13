# Address plan and hardware choices shared by the host and its VMs, so a
# value is written once. Each VM link is routed through the host on its own tap: no bridge, so
# a VM can only ever talk to the host, never put frames on another VM's link.
let
  # A ULA /48 for everything internal (RFC 4193; random Global ID).
  ula = "fd5e:2c8a:91d4";
in {
  inherit ula;

  # The Pi's interfaces as the vendor kernel names them. Check with `ip link`
  # on the Pi and adjust.
  host = {
    uplink = "wlan0"; # onboard Wi-Fi, client of the ISP router
    ethernet = "end0"; # handed to the wg VM whole, for the wired home LAN
  };

  # Public services VM: IPv4 only, internet only.
  public = {
    tap = "vm-public";
    mac = "02:00:00:00:01:02";
    hostV4 = "10.10.1.1";
    vmV4 = "10.10.1.2";
  };

  # Home router and VPN endpoint VM.
  wg = {
    tap = "vm-wg";
    mac = "02:00:00:00:02:02";
    hostV4 = "10.10.2.1";
    vmV4 = "10.10.2.2";
    hostV6 = "${ula}:2::1";
    vmV6 = "${ula}:2::2";

    lanTap = "vm-wg-lan"; # macvtap on host.ethernet
    lanMac = "02:00:00:00:50:01";

    # Where home Wi-Fi comes from (design doc, §9.2):
    #   "usb"           a USB adapter passed to this VM runs the AP (hostapd.nix)
    #   "access-point"  a separate access point, in AP mode with its DHCP off,
    #                   cabled to the Pi's Ethernet port; the VM serves Ethernet only
    homeWifi = "usb";

    # The USB adapter, when homeWifi = "usb"; `lsusb` shows vendor:product.
    # The values are a MediaTek MT7921AU's.
    apUsb = {
      vendor = "0e8d";
      product = "7961";
      driver = "mt7921u";
    };
    lanV4 = "192.168.50.1";
    lanV4Prefix = "192.168.50.0/24";
    lanV6 = "${ula}:50::1";
    lanV6Prefix = "${ula}:50::/64";

    tunnelV4 = "10.10.100.1";
    tunnelV4Prefix = "10.10.100.0/24";
    tunnelV6 = "${ula}:100::1";
    tunnelV6Prefix = "${ula}:100::/64";

    port = 51820;
  };

  # Resolvers the VMs use; never the ISP router.
  upstreamDns = [ "1.1.1.1" "9.9.9.9" "2606:4700:4700::1111" "2620:fe::fe" ];
}
