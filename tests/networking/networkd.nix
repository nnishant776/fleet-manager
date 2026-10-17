# Test of the `networkd` backend on a host built from this flake's modules
# (tests/networking/host): systemd-networkd and wpa_supplicant, no
# NetworkManager, and two Wi-Fi client interfaces on two different networks.
# Two routers on simulated radios, each in its own network namespace, offer
# WPA2/WPA3 and DHCP (tests/lib/hwsim-router.nix): "home net" and "office".
# The radios appear only after boot, as a USB adapter plugged in later would.
# The networks are put in /etc/wpa_supplicant/imperative.conf, the first in
# the form the Raspberry Pi image's first boot writes it. The test checks that:
# - each client starts when its radio appears, without holding up the boot;
# - each named interface starts with a copy of imperative.conf, then keeps
#   its own networks: `save_config` on one writes only its own file;
# - each joins its own router and gets IPv4 by DHCP;
# - the lowest routeMetric carries the default route;
# - stopping one client leaves the other's wpa_cli working;
# - the files stay private, and only privileged users drive wpa_cli;
# - both reconnect after a reboot.
# Run it with `nix build .#checks.x86_64-linux.networking-networkd`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "netd"; hostDir = ./host; };

  # Quotes, a backslash, a dollar and a hash: what a hand-written card file
  # may hold.
  homePassphrase = ''say "hi" \ $5 #1'';
  routerHome = import ../lib/hwsim-router.nix { inherit pkgs; } {
    ssid = "home net";
    passphrase = homePassphrase;
    iface = "wlan0";
    ns = "home";
    subnet = "192.168.77";
  };
  routerOffice = import ../lib/hwsim-router.nix { inherit pkgs; } {
    ssid = "office";
    passphrase = "office-pass-1";
    iface = "wlan1";
    ns = "office";
    subnet = "192.168.78";
    channel = 11;
  };

  # As lib/apps/rpi-image/firstboot-script.nix writes it: the SSID in hex,
  # the passphrase quoted, WPA2 and WPA3 both allowed. Then the office.
  hex = s: lib.concatMapStrings
    (c: lib.fixedWidthString 2 "0" (lib.toLower (lib.toHexString (lib.strings.charToInt c))))
    (lib.stringToCharacters s);
  networksFile = pkgs.writeText "imperative.conf" ''
    network={
    	ssid=${hex "home net"}
    	psk="${homePassphrase}"
    	key_mgmt=WPA-PSK WPA-PSK-SHA256 SAE
    	ieee80211w=1
    }
    network={
    	ssid=${hex "office"}
    	psk="office-pass-1"
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

      # Four simulated radios, loaded by the test after boot: wlan0 and wlan1
      # for the routers, wlan2 and wlan3 for the host.
      boot.extraModprobeConfig = "options mac80211_hwsim radios=4";
      # The VM module turns Wi-Fi off in every test VM (mkVMOverride,
      # priority 10); this one has radios.
      networking.wireless.enable = lib.mkOverride 9 true;
    }];
  };

  testScript = ''
    def radios():
        netd.succeed("modprobe mac80211_hwsim")
        # Polled rather than wait_for_unit, which gives up on a unit with no
        # start job yet: the job comes once udev has announced the radio.
        for i in ["wlan2", "wlan3"]:
            netd.wait_until_succeeds(f"systemctl is-active wpa_supplicant-{i}.service", timeout=60)

    def radios_and_routers():
        radios()
        netd.succeed("${routerHome}/bin/hwsim-router")
        netd.succeed("${routerOffice}/bin/hwsim-router")

    def addresses():
        netd.wait_until_succeeds("ip -4 -br addr show wlan2 | grep -q 192.168.77.", timeout=120)
        netd.wait_until_succeeds("ip -4 -br addr show wlan3 | grep -q 192.168.78.", timeout=120)

    netd.start()
    netd.wait_for_unit("multi-user.target")

    with subtest("networkd and wpa_supplicant, no NetworkManager"):
        netd.fail("systemctl cat NetworkManager.service")
        netd.succeed("systemctl is-active systemd-networkd.service")

    with subtest("a client whose radio is missing waits for it, and starts when it appears"):
        netd.fail("systemctl is-active wpa_supplicant-wlan2.service")
        radios()

    netd.succeed("install -m 0664 ${networksFile} /etc/wpa_supplicant/imperative.conf")
    netd.succeed("rm -f /etc/wpa_supplicant/wlan2.conf /etc/wpa_supplicant/wlan3.conf")
    netd.succeed("systemctl restart wpa_supplicant-wlan2.service wpa_supplicant-wlan3.service")
    # systemctl returns once wpa_supplicant is launched; its control socket
    # comes a moment later.
    for i in ["wlan2", "wlan3"]:
        netd.wait_until_succeeds(f"wpa_cli -i {i} ping | grep -qx PONG", timeout=30)

    with subtest("a newly named interface starts with a copy of imperative.conf"):
        for i in ["wlan2", "wlan3"]:
            netd.succeed(f"cmp /etc/wpa_supplicant/imperative.conf /etc/wpa_supplicant/{i}.conf")

    with subtest("each interface keeps its own networks"):
        # wlan2 keeps the home network (0), wlan3 the office (1).
        netd.succeed("wpa_cli -i wlan2 remove_network 1 && wpa_cli -i wlan2 save_config")
        netd.succeed("wpa_cli -i wlan3 remove_network 0 && wpa_cli -i wlan3 save_config")
        netd.succeed('test "$(grep -c network= /etc/wpa_supplicant/wlan2.conf)" = 1')
        netd.succeed('test "$(grep -c network= /etc/wpa_supplicant/wlan3.conf)" = 1')
        netd.succeed('test "$(grep -c network= /etc/wpa_supplicant/imperative.conf)" = 2')

    netd.succeed("${routerHome}/bin/hwsim-router")
    netd.succeed("${routerOffice}/bin/hwsim-router")

    with subtest("each joins its own router and gets IPv4 by DHCP"):
        addresses()
        netd.succeed("wpa_cli -i wlan2 status | grep -qx 'ssid=home net'")
        netd.succeed("wpa_cli -i wlan3 status | grep -qx ssid=office")
        netd.succeed("ping -c1 -W2 192.168.77.1")
        netd.succeed("ping -c1 -W2 192.168.78.1")
        netd.succeed('test "$(wpa_cli -i wlan2 get country)" = IN')

    with subtest("the lowest route metric carries the default route"):
        # wlan2 sets 512; wlan3 and the VM's wired card keep networkd's 1024.
        netd.wait_until_succeeds("ip -4 route show default dev wlan2 | grep -q 'metric 512'")
        netd.succeed("ip -4 route show default dev wlan3 | grep -q 'metric 1024'")
        print(netd.succeed("ip -4 route show default"))
        netd.succeed("ip -4 route get 1.1.1.1 | grep -q 'dev wlan2'")

    with subtest("stopping one client leaves the other's wpa_cli working"):
        netd.succeed("systemctl stop wpa_supplicant-wlan2.service")
        netd.succeed("wpa_cli -i wlan3 status | grep -q wpa_state=COMPLETED")
        netd.succeed("systemctl start wpa_supplicant-wlan2.service")

    with subtest("the network files are private to wpa_supplicant"):
        netd.succeed('test "$(stat -c %a /etc/wpa_supplicant)" = 750')
        for f in ["imperative", "wlan2", "wlan3"]:
            netd.fail(f"runuser -u bob -- cat /etc/wpa_supplicant/{f}.conf")
            netd.succeed(f'test "$(stat -c %a /etc/wpa_supplicant/{f}.conf)" = 660')

    with subtest("privileged users drive wpa_cli, others cannot"):
        netd.wait_until_succeeds("runuser -u admin -- wpa_cli -i wlan2 status | grep -q wpa_state=COMPLETED")
        netd.fail("runuser -u bob -- wpa_cli -i wlan2 status")

    with subtest("both reconnect on their own after a reboot"):
        netd.shutdown()
        netd.start()
        netd.wait_for_unit("multi-user.target")
        radios_and_routers()
        addresses()
  '';
}
