# Test of several hotspots on a host built from this flake's modules
# (tests/networking/hotspot-host), each on an adapter of its own and in a
# service of its own. Nine simulated radios: a router in its own network
# namespace (tests/lib/hwsim-router.nix), the host's Wi-Fi uplink, hotspots A
# (5 GHz), B (6 GHz, one device at a time) and E (a custom line hostapd
# rejects), and four "phones", each in a namespace of its own. Hotspot C is
# turned on for an adapter that is not there. The test checks that:
# - each hotspot runs alone: C waits for its adapter, E fails, and neither
#   holds up A or B; stopping A leaves B's phone connected;
# - phones join with WPA3 (on 6 GHz with protected management frames, under
#   India's rules), get an address by DHCP and reach the router through NAT,
#   which has no route back to the hotspots' subnets;
# - two phones on the same hotspot reach each other;
# - B's custom setting (one device at a time) reaches hostapd.
# Run it with `nix build .#checks.x86_64-linux.networking-hotspot`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNode = mkHost.modulesFor { hostName = "hotspots"; hostDir = ./hotspot-host; };

  radio = n: "wlx02000000${lib.fixedWidthString 2 "0" (toString n)}00";
  hotspotA = radio 2;
  hotspotB = radio 3;
  hotspotC = "wlx020000009900";
  hotspotE = radio 8;
  uplink = radio 1;

  router = import ../lib/hwsim-router.nix { inherit pkgs; } {
    ssid = "home net";
    passphrase = "router-pass-1";
    iface = radio 0;
  };
  # The uplink's network, as the networkd backend keeps it ("home net" in hex).
  uplinkNetwork = pkgs.writeText "uplink.conf" ''
    network={
    	ssid=686f6d65206e6574
    	psk="router-pass-1"
    	key_mgmt=WPA-PSK WPA-PSK-SHA256 SAE
    	ieee80211w=1
    }
  '';

  password = "hotspot-pass-1";

  # A phone: a radio in a namespace of its own, joining a hotspot with WPA3.
  # On 6 GHz it is told where to look, as scanning that band passively is
  # slow.
  phone = { name, n, ssid, freq ? null }:
    let
      conf = pkgs.writeText "${name}.conf" ''
        sae_pwe=2
        network={
        	ssid="${ssid}"
        	key_mgmt=SAE
        	sae_password="${password}"
        	ieee80211w=2
        	${lib.optionalString (freq != null) "scan_freq=${toString freq}"}
        }
      '';
    in pkgs.writeShellApplication {
      name = "phone-${name}";
      runtimeInputs = with pkgs; [ iw iproute2 gawk wpa_supplicant ];
      text = ''
        phy=$(iw dev ${radio n} info | awk '/wiphy/ { print "phy" $2 }')
        ip netns add ${name}
        iw phy "$phy" set netns name ${name}
        ip netns exec ${name} ip link set lo up
        ip netns exec ${name} wpa_supplicant -B -i ${radio n} -c ${conf}
      '';
    };
  phones = [
    (phone { name = "a1"; n = 4; ssid = "pi a"; })
    (phone { name = "a2"; n = 5; ssid = "pi a"; })
    (phone { name = "b1"; n = 6; ssid = "pi b"; freq = 6135; })
    (phone { name = "b2"; n = 7; ssid = "pi b"; freq = 6135; })
  ];

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
in
pkgs.testers.runNixOSTest {
  name = "networking-hotspot";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = hostNode.specialArgs;
  node.pkgsReadOnly = false;

  nodes.hotspots = {
    imports = hostNode.modules ++ [{
      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      boot.kernelModules = [ "mac80211_hwsim" ];
      boot.extraModprobeConfig = "options mac80211_hwsim radios=9";
      # The VM module turns Wi-Fi off in every test VM (mkVMOverride,
      # priority 10); this one has radios.
      networking.wireless.enable = lib.mkOverride 9 true;

      environment.systemPackages = [ router pkgs.iw ] ++ phones;
      # Stands in for the root-only files an admin creates before turning
      # the hotspots on.
      systemd.tmpfiles.rules = map (h: "f /var/lib/hotspot/${h} 0600 root root - ${password}")
        [ hotspotA hotspotB hotspotC hotspotE ];
    }];
  };

  testScript = ''
    m = hotspots

    def join(name, subnet):
        m.succeed(f"phone-{name}")
        dev = {"a1": "${radio 4}", "a2": "${radio 5}", "b1": "${radio 6}"}[name]
        m.wait_until_succeeds(f"ip netns exec {name} iw dev {dev} link | grep -q 'Connected to'", timeout=180)
        m.wait_until_succeeds(
            f"ip netns exec {name} ${pkgs.busybox}/bin/busybox udhcpc"
            f" -i {dev} -n -q -t 5 -s ${dhcpScript}",
            timeout=120,
        )
        m.succeed(f"ip netns exec {name} ip -4 -br addr show {dev} | grep -q {subnet}")

    m.wait_for_unit("multi-user.target")
    m.succeed("udevadm settle")

    with subtest("each hotspot runs in a service of its own"):
        # Polled: each service's start job comes with its adapter's device.
        for h in ["${hotspotA}", "${hotspotB}"]:
            m.wait_until_succeeds(f"systemctl is-active hostapd-{h}.service", timeout=60)
        m.wait_until_succeeds("iw dev ${hotspotA} info | grep -q 'channel 36 (5180 MHz)'", timeout=60)
        m.wait_until_succeeds("iw dev ${hotspotB} info | grep -q 'channel 37 (6135 MHz)'", timeout=60)

    with subtest("a hotspot whose adapter is missing waits, holding up nothing"):
        m.succeed('test "$(systemctl show -P ActiveState hostapd-${hotspotC}.service)" = inactive')

    with subtest("a hotspot whose custom line hostapd rejects fails alone"):
        m.wait_until_succeeds(
            "journalctl -u hostapd-${hotspotE}.service | grep -q 'unknown configuration item'",
            timeout=60,
        )
        m.fail("systemctl is-active hostapd-${hotspotE}.service")
        m.succeed("systemctl is-active hostapd-${hotspotA}.service hostapd-${hotspotB}.service")

    m.succeed("install -m 0660 ${uplinkNetwork} /etc/wpa_supplicant/${uplink}.conf")
    m.succeed("systemctl restart wpa_supplicant-${uplink}.service")
    m.succeed("hwsim-router")
    m.wait_until_succeeds("ip -4 -br addr show ${uplink} | grep -q 192.168.77.", timeout=120)

    with subtest("phones join with WPA3 and get an address"):
        join("a1", "10.42.0.")
        join("a2", "10.42.0.")
        join("b1", "10.43.0.")

    with subtest("two phones on the same hotspot reach each other"):
        a2 = m.succeed("ip netns exec a2 ip -4 -o addr show ${radio 5} | awk '{print $4}' | cut -d/ -f1").strip()
        m.succeed(f"ip netns exec a1 ping -c1 -W3 {a2}")

    with subtest("phones reach the router through the host"):
        for name in ["a1", "a2", "b1"]:
            m.succeed(f"ip netns exec {name} ping -c1 -W3 192.168.77.1")

    with subtest("stopping one hotspot leaves the others running"):
        m.succeed("systemctl stop hostapd-${hotspotA}.service")
        m.succeed("ip netns exec b1 iw dev ${radio 6} link | grep -q 'Connected to'")
        m.succeed("ip netns exec b1 ping -c1 -W3 192.168.77.1")
        m.succeed("systemctl start hostapd-${hotspotA}.service")

    with subtest("B's custom setting reaches hostapd: one device at a time"):
        m.succeed("phone-b2")
        m.sleep(30)
        m.fail("ip netns exec b2 iw dev ${radio 7} link | grep -q 'Connected to'")
        m.succeed("ip netns exec b1 iw dev ${radio 6} link | grep -q 'Connected to'")
  '';
}
