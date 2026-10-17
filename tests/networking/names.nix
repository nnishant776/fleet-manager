# Test of conf.networking.macNames on a host built from this flake's modules
# (tests/networking/names-host). Two VMs, each with a network card on PCI
# and two simulated radios:
# - "named" has macNames on. Its own card is named by systemd in the initrd
#   first (predictable names are turned back on, as on a real host), and must
#   still end up named after its MAC; its other card and its radios too.
#   Interfaces a program creates with a name of its own keep it, and the
#   names are the same after a reboot.
# - "plain" has macNames off: the kernel's names stay.
# Run it with `nix build .#checks.x86_64-linux.networking-names`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;
  mkHost = import ../../lib/mkHost.nix { inherit inputs; };
  hostNodeFor = name: mkHost.modulesFor { hostName = name; hostDir = ./names-host; };

  node = name: macNames: {
    imports = (hostNodeFor name).modules ++ [{
      conf.host.name = lib.mkForce name;
      conf.networking.macNames = lib.mkForce macNames;
      # Test VMs turn predictable names off; "named" turns them back on, so
      # the initrd names its card before the real system renames it.
      networking.usePredictableInterfaceNames = lib.mkForce macNames;

      # No ESP and no bootable disk in the test VM.
      boot.loader.systemd-boot.enable = lib.mkForce false;
      boot.loader.grub.enable = lib.mkForce false;

      boot.kernelModules = [ "mac80211_hwsim" ];
      boot.extraModprobeConfig = "options mac80211_hwsim radios=2";
      environment.systemPackages = [ pkgs.iw ];
    }];
  };
in
pkgs.testers.runNixOSTest {
  name = "networking-names";

  # As in tests/secrets/vm-test.nix: the host modules take flakeLib as a
  # module argument, and set nixpkgs.config themselves.
  node.specialArgs = (hostNodeFor "named").specialArgs;
  node.pkgsReadOnly = false;

  # Node numbers follow the names' order: named is 1, plain is 2, which is in
  # each card's MAC, 52:54:00:12:01:0<n>.
  nodes.named = node "named" true;
  nodes.plain = node "plain" false;

  testScript = ''
    def has(m, name):
        m.succeed(f"ip link show {name}")

    def check_named():
        named.wait_for_unit("multi-user.target")
        named.succeed("udevadm settle")
        # The VM's own card has QEMU's default address 52:54:00:12:34:56, the
        # test network's card 52:54:00:12:01:01; mac80211_hwsim gives radio N
        # the address 02:00:00:00:0N:00.
        for name in ["enp525400123456", "enp525400120101", "wlx020000000000", "wlx020000000100"]:
            has(named, name)
        named.fail("ls /sys/class/net | grep -E '^(eth|wlan|enp0s)'")

    start_all()

    with subtest("named: the card and the radios are named after their MAC"):
        check_named()

    with subtest("named: a card the initrd named is renamed all the same"):
        # The initrd names the VM's own card after its slot (ens3, say); the
        # test network's card appears too late for it and is renamed from eth1.
        named.succeed("dmesg | grep -E 'enp525400123456: renamed from en[ops][0-9]'")

    with subtest("named: interfaces a program names keep their names"):
        named.succeed("ip link add vtest0 type veth peer name vtest1")
        named.succeed("ip link add br-test type bridge")
        named.succeed(
            "iw phy $(cat /sys/class/net/wlx020000000000/phy80211/name)"
            " interface add test0 type station"
        )
        named.succeed("udevadm settle")
        for name in ["vtest0", "vtest1", "br-test", "test0"]:
            has(named, name)

    with subtest("named: the same names after a reboot"):
        named.shutdown()
        named.start()
        check_named()

    with subtest("plain: macNames off keeps the kernel's names"):
        plain.wait_for_unit("multi-user.target")
        plain.succeed("udevadm settle")
        for name in ["eth1", "wlan0", "wlan1"]:
            has(plain, name)
        plain.fail("ls /sys/class/net | grep -E '^(enp|enx|wlx)'")
  '';
}
