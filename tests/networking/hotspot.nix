# Test of the hotspot on a host built from this flake's modules
# (tests/networking/hotspot-host), once on 5 GHz and once on 6 GHz. Each VM
# has four simulated radios: a router in its own network namespace
# (tests/lib/hwsim-router.nix), the host's Wi-Fi uplink, the host's hotspot
# adapter (picked by MAC address) and a "phone" in another namespace. The test
# checks that the adapter is reserved under its name, that hostapd runs the
# hotspot on the right channel, that the phone joins with WPA3 (on 6 GHz with
# protected management frames, under India's rules) and gets an address by
# DHCP, and that it reaches the router through the host. The router has no
# route back to the hotspot's subnet, so that only works through NAT.
# Run it with `nix build .#checks.x86_64-linux.networking-hotspot`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNodeFor = name: mkHost.modulesFor { hostName = name; hostDir = ./hotspot-host; };

  router = import ../lib/hwsim-router.nix { inherit pkgs; } {
    ssid = "home net";
    passphrase = "router-pass-1";
  };
  # The uplink's network, as the networkd backend keeps it ("home net" in hex).
  uplinkNetwork = pkgs.writeText "imperative.conf" ''
    network={
    	ssid=686f6d65206e6574
    	psk="router-pass-1"
    	key_mgmt=WPA-PSK WPA-PSK-SHA256 SAE
    	ieee80211w=1
    }
  '';

  password = "hotspot-pass-1";

  # A phone on the fourth radio, in its own network namespace, joining the
  # hotspot with WPA3. On 6 GHz it is told where to look, as scanning that
  # band passively takes long.
  phoneConf = band: pkgs.writeText "phone.conf" ''
    sae_pwe=2
    network={
    	ssid="pi hotspot"
    	key_mgmt=SAE
    	sae_password="${password}"
    	ieee80211w=2
    	${lib.optionalString (band == "6g") "scan_freq=6135"}
    }
  '';
  phone = band: pkgs.writeShellApplication {
    name = "hwsim-phone";
    runtimeInputs = with pkgs; [ iw iproute2 gawk wpa_supplicant ];
    text = ''
      phy=$(iw dev wlan3 info | awk '/wiphy/ { print "phy" $2 }')
      ip netns add phone
      iw phy "$phy" set netns name phone
      ip netns exec phone ip link set lo up
      ip netns exec phone wpa_supplicant -B -i wlan3 -c ${phoneConf band}
    '';
  };
  # busybox udhcpc applies nothing itself; this sets the address and route.
  dhcpScript = pkgs.writeShellScript "phone-dhcp" ''
    case "$1" in
      bound|renew)
        ${pkgs.iproute2}/bin/ip addr flush dev "$interface"
        ${pkgs.iproute2}/bin/ip addr add "$ip/$mask" dev "$interface"
        ${pkgs.iproute2}/bin/ip route replace default via "''${router%% *}" dev "$interface"
        ;;
    esac
  '';

  node = name: band: {
    imports = (hostNodeFor name).modules ++ [{
      conf.host.name = lib.mkForce name;
      conf.networking.wireless.hotspot.band = lib.mkForce band;

      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      boot.kernelModules = [ "mac80211_hwsim" ];
      boot.extraModprobeConfig = "options mac80211_hwsim radios=4";
      # The VM module turns Wi-Fi off in every test VM (mkVMOverride,
      # priority 10); this one has radios.
      networking.wireless.enable = lib.mkOverride 9 true;

      environment.systemPackages = [ router (phone band) pkgs.iw ];
      # Stands in for the root-only file an admin creates before turning the
      # hotspot on.
      systemd.tmpfiles.rules = [ "f /var/lib/hotspot/password 0600 root root - ${password}" ];
    }];
  };
in
pkgs.testers.runNixOSTest {
  name = "networking-hotspot";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = (hostNodeFor "hotspot5").specialArgs;
  node.pkgsReadOnly = false;

  nodes.hotspot5 = node "hotspot5" "5g";
  nodes.hotspot6 = node "hotspot6" "6g";

  testScript = ''
    def check(m, channel):
        m.wait_for_unit("multi-user.target")

        with subtest(f"{m.name}: the adapter is reserved and the hotspot runs"):
            m.succeed('test "$(cat /sys/class/net/wlan-ap/address)" = 02:00:00:00:02:00')
            m.wait_for_unit("hostapd.service")
            m.wait_until_succeeds(f"iw dev wlan-ap info | grep -q '{channel}'", timeout=60)

        m.succeed("install -m 0660 ${uplinkNetwork} /etc/wpa_supplicant/imperative.conf")
        m.succeed("systemctl restart wpa_supplicant-wlan1.service")
        m.succeed("hwsim-router")
        m.wait_until_succeeds("ip -4 -br addr show wlan1 | grep -q 192.168.77.", timeout=120)

        with subtest(f"{m.name}: a phone joins with WPA3 and gets an address"):
            m.succeed("hwsim-phone")
            m.wait_until_succeeds("ip netns exec phone iw dev wlan3 link | grep -q 'Connected to'", timeout=180)
            m.wait_until_succeeds(
                "ip netns exec phone ${pkgs.busybox}/bin/busybox udhcpc"
                " -i wlan3 -n -q -t 5 -s ${dhcpScript}",
                timeout=120,
            )
            m.succeed("ip netns exec phone ip -4 -br addr show wlan3 | grep -q 10.42.0.")

        with subtest(f"{m.name}: the phone reaches the router through the host"):
            m.succeed("ip netns exec phone ping -c1 -W3 192.168.77.1")

    start_all()
    check(hotspot5, "channel 36 (5180 MHz)")
    check(hotspot6, "channel 37 (6135 MHz)")
  '';
}
