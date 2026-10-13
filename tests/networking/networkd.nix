# Test of the `networkd` backend on a host built from this flake's modules
# (tests/networking/host): systemd-networkd and wpa_supplicant, no
# NetworkManager. A router on one simulated radio, in its own network
# namespace, offers WPA2/WPA3 and DHCP (tests/lib/hwsim-router.nix). The
# host's Wi-Fi network is put in /etc/wpa_supplicant/imperative.conf in the
# form the Raspberry Pi image's first boot writes it, and the test checks that
# the host joins, gets IPv4 by DHCP, keeps the file private, lets only
# privileged users drive wpa_cli, and reconnects after a reboot.
# Run it with `nix build .#checks.x86_64-linux.networking-networkd`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "netd"; hostDir = ./host; };

  ssid = "home net";
  # Quotes, a backslash, a dollar and a hash: what a hand-written card file
  # may hold.
  passphrase = ''say "hi" \ $5 #1'';
  router = import ../lib/hwsim-router.nix { inherit pkgs; } { inherit ssid passphrase; };

  # As lib/apps/rpi-image/firstboot-script.nix writes it: the SSID in hex,
  # the passphrase quoted, WPA2 and WPA3 both allowed.
  ssidHex = lib.concatMapStrings
    (c: lib.fixedWidthString 2 "0" (lib.toLower (lib.toHexString (lib.strings.charToInt c))))
    (lib.stringToCharacters ssid);
  networkFile = pkgs.writeText "imperative.conf" ''
    network={
    	ssid=${ssidHex}
    	psk="${passphrase}"
    	key_mgmt=WPA-PSK WPA-PSK-SHA256 SAE
    	ieee80211w=1
    }
  '';
in
pkgs.testers.runNixOSTest {
  name = "networking-networkd";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.netd = {
    imports = hostNode.modules ++ [{
      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      # Two simulated radios: wlan0 for the router, wlan1 for the host.
      boot.kernelModules = [ "mac80211_hwsim" ];
      # The VM module turns Wi-Fi off in every test VM (mkVMOverride,
      # priority 10); this one has radios.
      networking.wireless.enable = lib.mkOverride 9 true;

      environment.systemPackages = [ router ];
    }];
  };

  testScript = ''
    def router_and_address():
        netd.succeed("hwsim-router")
        netd.wait_until_succeeds("ip -4 -br addr show wlan1 | grep -q 192.168.77.", timeout=120)

    netd.start()
    netd.wait_for_unit("multi-user.target")

    with subtest("networkd and wpa_supplicant, no NetworkManager"):
        netd.fail("systemctl cat NetworkManager.service")
        netd.succeed("systemctl is-active systemd-networkd.service")
        netd.wait_for_unit("wpa_supplicant-wlan1.service")

    netd.succeed("install -m 0664 ${networkFile} /etc/wpa_supplicant/imperative.conf")
    netd.succeed("systemctl restart wpa_supplicant-wlan1.service")

    with subtest("joins the router's Wi-Fi and gets IPv4 by DHCP"):
        router_and_address()
        netd.succeed("ping -c1 -W2 192.168.77.1")
        netd.succeed('test "$(wpa_cli -i wlan1 get country)" = IN')

    with subtest("the network file is private to wpa_supplicant"):
        netd.succeed('test "$(stat -c %a /etc/wpa_supplicant/imperative.conf)" = 660')

    with subtest("privileged users drive wpa_cli, others cannot"):
        netd.succeed("runuser -u admin -- wpa_cli -i wlan1 status | grep -q wpa_state=COMPLETED")
        netd.fail("runuser -u bob -- wpa_cli -i wlan1 status")

    with subtest("reconnects on its own after a reboot"):
        netd.shutdown()
        netd.start()
        netd.wait_for_unit("multi-user.target")
        router_and_address()
  '';
}
