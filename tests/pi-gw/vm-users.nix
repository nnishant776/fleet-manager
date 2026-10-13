# Test of pi-gw's per-VM host users (design doc, N1) on two small x86_64
# microVMs built with the same modules as pi-gw's: vm-users.nix on the host and
# vms/host-user.nix in each guest. VM `a` has one tap without vhost, like the
# public VM; VM `b` has a multi-queue tap with vhost and a macvtap, like the wg
# VM. The test checks that both boot and answer under the confinement, that
# each VM's QEMU, state directory, tap and macvtap belong to its own user, and
# that `a`'s user can neither read `b`'s disk, nor open its macvtap, nor attach
# to its tap. The guests run under nested KVM.
# Run it with `nix build .#checks.x86_64-linux.pi-gw-vm-users`.
{ pkgs, inputs }:
let
  lib = pkgs.lib;

  # Attaches a queue to an existing multi-queue tap the way QEMU does, then
  # lets go; fails if the kernel refuses.
  tunAttach = pkgs.writers.writePython3Bin "tun-attach" { } ''
    import fcntl
    import os
    import struct
    import sys

    TUNSETIFF = 0x400454CA
    IFF_TAP = 0x0002
    IFF_MULTI_QUEUE = 0x0100
    IFF_NO_PI = 0x1000
    IFF_VNET_HDR = 0x4000

    flags = IFF_TAP | IFF_NO_PI | IFF_VNET_HDR | IFF_MULTI_QUEUE
    fd = os.open("/dev/net/tun", os.O_RDWR)
    ifr = struct.pack("16sH", sys.argv[1].encode(), flags)
    fcntl.ioctl(fd, TUNSETIFF, ifr)
    os.close(fd)
  '';

  guest = { name, index, vcpu, vhost, lan ? false }: {
    imports = [ ../../hosts/pi-gw/vms/host-user.nix ];
    system.stateVersion = "26.05";

    microvm = {
      hypervisor = "qemu";
      inherit vcpu;
      mem = 512;
      interfaces = [{
        type = "tap";
        id = "vm-${name}";
        mac = "02:00:00:00:0${toString index}:02";
        tap.vhost = vhost;
      }] ++ lib.optional lan {
        type = "macvtap";
        id = "vm-${name}-lan";
        mac = "02:00:00:00:0${toString index}:03";
        macvtap = {
          link = "eth1";
          mode = "bridge";
        };
      };
      volumes = [{
        image = "var.img";
        mountPoint = "/var";
        size = 64;
      }];
    };

    networking.useNetworkd = true;
    networking.useDHCP = false;
    systemd.network.networks."10-uplink" = {
      matchConfig.MACAddress = "02:00:00:00:0${toString index}:02";
      address = [ "10.10.${toString index}.2/24" ];
    };
  };
in
pkgs.testers.runNixOSTest {
  name = "pi-gw-vm-users";

  nodes.host = {
    imports = [
      inputs.microvm.nixosModules.host
      ../../hosts/pi-gw/vm-users.nix
    ];

    virtualisation = {
      cores = 2;
      memorySize = 2048;
    };
    environment.systemPackages = [ tunAttach ];

    # As on pi-gw, networkd owns the VM links only.
    systemd.network = {
      enable = true;
      wait-online.enable = false;
      networks."30-vm-a" = {
        matchConfig.Name = "vm-a";
        address = [ "10.10.1.1/24" ];
        linkConfig.RequiredForOnline = "no";
      };
      networks."30-vm-b" = {
        matchConfig.Name = "vm-b";
        address = [ "10.10.2.1/24" ];
        linkConfig.RequiredForOnline = "no";
      };
    };

    microvm.vms = {
      a.config = guest { name = "a"; index = 1; vcpu = 1; vhost = false; };
      b.config = guest { name = "b"; index = 2; vcpu = 2; vhost = true; lan = true; };
    };
  };

  testScript = ''
    start_all()
    host.wait_for_unit("microvm@a.service")
    host.wait_for_unit("microvm@b.service")

    with subtest("both VMs boot and answer on their links, confined"):
        host.wait_until_succeeds("ping -c1 -W1 10.10.1.2", timeout=300)
        host.wait_until_succeeds("ping -c1 -W1 10.10.2.2", timeout=300)

    with subtest("each VM's QEMU and resources belong to its own user"):
        for vm in ["a", "b"]:
            user = f"microvm-{vm}"
            pid = host.succeed(f"systemctl show -P MainPID microvm@{vm}").strip()
            host.succeed(f'test "$(stat -c %U /proc/{pid})" = {user}')
            host.succeed(
                f'test "$(stat -c %U:%G:%a /var/lib/microvms/{vm})"'
                f" = {user}:{user}:700"
            )
            host.succeed(f'test "$(stat -c %U /var/lib/microvms/{vm}/var.img)" = {user}')
            host.succeed(f'test "$(cat /sys/class/net/vm-{vm}/owner)" = "$(id -u {user})"')
        host.succeed(
            'test "$(stat -c %U /dev/tap$(cat /sys/class/net/vm-b-lan/ifindex))" = microvm-b'
        )

    with subtest("a's user cannot reach b's disk, macvtap or tap"):
        host.fail("runuser -u microvm-a -- head -c1 /var/lib/microvms/b/var.img")
        host.fail(
            "runuser -u microvm-a -- sh -c"
            " 'exec 3<>/dev/tap$(cat /sys/class/net/vm-b-lan/ifindex)'"
        )
        host.fail("runuser -u microvm-a -- tun-attach vm-b")
        # The refusal is about the user: b's own user may attach a queue.
        host.succeed("runuser -u microvm-b -- tun-attach vm-b")
  '';
}
