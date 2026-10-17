# conf.networking.macNames: every physical network interface is named after
# what it is, its bus and its MAC address, so the same adapter has the same
# name on every boot and in any port:
#
#   wl / en    Wi-Fi / wired
#   u p s o x  USB, PCI, SDIO, onboard (a device the board describes, such
#              as the Pi 5's built-in Ethernet), anything else (simulated
#              radios)
#   the MAC    without colons: wlu001a2b3c4d5e (15 characters, Linux's limit)
#
# Left alone: virtual interfaces (bridges, bonds, VLANs, veth, tap, VPNs,
# container and VM networks), interfaces a program created and named itself,
# and adapters without a burned-in MAC, which would get a new name each boot.
#
# The rule runs after systemd's naming rule (80-net-setup-link.rules sets a
# name only when none is set), and udev renames once with the last name set.
# Names udev itself gave (name_assign_type 4) are renamed again on purpose:
# the initrd names devices whose driver is built in (the Pi 5's Ethernet)
# before this rule exists, and the real system's replay of the device events
# must still rename them. That replay comes before anything brings the
# interface up only with the systemd initrd, which cleans up udev's records
# when it hands over; hence the assertion.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.networking;

  # "00:1a:2b:3c:4d:5e" -> "001a2b3c4d5e"
  macHex = pkgs.writeShellScript "mac-hex" ''
    printf '%s' "$1" | ${pkgs.coreutils}/bin/tr -d :
  '';
in {
  config = lib.mkIf cfg.macNames {
    services.udev.extraRules = ''
      ACTION!="add", GOTO="mac_names_end"
      SUBSYSTEM!="net", GOTO="mac_names_end"
      # Leave names a program chose.
      ATTR{name_assign_type}=="3", GOTO="mac_names_end"
      # Only a burned-in address; a random one would change the name each boot.
      ATTR{addr_assign_type}!="0", GOTO="mac_names_end"

      # Wi-Fi, simulated radios included.
      ENV{DEVTYPE}=="wlan", ENV{MAC_NAME_KIND}="wl", GOTO="mac_names_bus"
      # Wired: physical Ethernet only. Bridges, bonds, VLANs and modems set a
      # DEVTYPE; veth, tap and dummy devices live under /devices/virtual.
      ENV{DEVTYPE}=="?*", GOTO="mac_names_end"
      ATTR{type}!="1", GOTO="mac_names_end"
      DEVPATH=="/devices/virtual/*", GOTO="mac_names_end"
      ENV{MAC_NAME_KIND}="en"

      LABEL="mac_names_bus"
      # The bus, from the device's ancestors. Later lines win, so a USB adapter
      # behind a PCI controller ends up "u", and SDIO behind PCI ends up "s".
      # Onboard comes first: PCI, SDIO and USB controllers sit on the board too.
      ENV{MAC_NAME_BUS}="x"
      SUBSYSTEMS=="platform", ENV{MAC_NAME_BUS}="o"
      SUBSYSTEMS=="pci", ENV{MAC_NAME_BUS}="p"
      SUBSYSTEMS=="sdio", ENV{MAC_NAME_BUS}="s"
      SUBSYSTEMS=="usb", ENV{MAC_NAME_BUS}="u"

      PROGRAM="${macHex} $attr{address}", NAME="$env{MAC_NAME_KIND}$env{MAC_NAME_BUS}$result"
      LABEL="mac_names_end"
    '';

    assertions = [{
      assertion = config.boot.initrd.systemd.enable;
      message = "conf.networking.macNames on '${config.conf.host.name}' needs the systemd initrd (boot.initrd.systemd.enable): with the scripted one, an interface the initrd named can be up before the real system renames it, and the rename then fails.";
    }];
  };
}
