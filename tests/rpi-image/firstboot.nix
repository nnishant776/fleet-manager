# Test of the rpi-image card's first boot on x86_64. The node has the image's
# network-relevant layers, from the same nixpkgs as the image: NixOS's
# installation-device profile (NetworkManager), the nixos-images installer
# (systemd-networkd, iwd) and lib/apps/rpi-image/firstboot.nix. The Pi board
# support and nixos-raspberrypi's installer module are left out.
# A FAT disk labelled FIRMWARE carries firstboot/ files written as on Windows
# (CRLF), and a simulated radio (mac80211_hwsim) runs a WPA2 access point.
# The test checks that the first boot sets the hostname, installs the SSH key
# and joins the Wi-Fi with NetworkManager alone, keeping the connection in /etc
# so it outlives the switch to a host.
# Run it with `nix build .#checks.x86_64-linux.rpi-image-firstboot`.
{ inputs }:
let
  # The image's nixpkgs, not this flake's, so the NetworkManager and hostnamed
  # modules are the ones on the card.
  pkgs = inputs.nixos-raspberrypi.inputs.nixpkgs.legacyPackages.x86_64-linux;
  nixos-images = inputs.nixos-raspberrypi.inputs.nixos-images;

  ssid = "rpi-test";
  psk = "correct horse battery";
  hostName = "pi-test";

  # What a card owner would generate on their workstation.
  sshKey = pkgs.runCommand "rpi-firstboot-key" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    ssh-keygen -q -t ed25519 -N "" -C owner -f $out/id_ed25519
  '';

  firmware = pkgs.runCommand "rpi-firstboot-firmware.img" {
    nativeBuildInputs = [ pkgs.dosfstools pkgs.mtools ];
  } ''
    mkdir firstboot
    printf '%s\r\n' ${hostName} > firstboot/hostname
    cp ${sshKey}/id_ed25519.pub firstboot/authorized_keys
    printf 'SSID=%s\r\nPSK=%s\r\n' ${ssid} '${psk}' > firstboot/wifi
    mkfs.vfat -C -n FIRMWARE $out 8192
    mcopy -s -i $out firstboot ::firstboot
  '';
in
pkgs.testers.runNixOSTest {
  name = "rpi-image-firstboot";

  # The installation-device profile adds a nixpkgs overlay.
  node.pkgsReadOnly = false;

  nodes.machine = { lib, modulesPath, ... }: {
    imports = [
      (modulesPath + "/profiles/installation-device.nix")
      "${nixos-images}/nix/image-installer/module.nix"
      ../../lib/apps/rpi-image/firstboot.nix
    ];

    # The VM module turns Wi-Fi off in every test VM (mkVMOverride, priority
    # 10); this one has radios, so restore what firstboot.nix sets on the card.
    # That firstboot.nix wins over nixos-raspberrypi's own mkForce false is
    # checked on the real image by rpi-image-config.
    networking.wireless.enable = lib.mkOverride 9 true;

    # The image logs root in on the console and runs a status screen that
    # redraws every two seconds; on the test's serial console that only floods
    # the log.
    services.getty.autologinUser = lib.mkOverride 10 null;

    # The card's FAT partition, mounted as the image mounts it.
    virtualisation.qemu.drives = [{
      name = "firmware";
      file = "${firmware}";
      driveExtraOpts = { format = "raw"; snapshot = "on"; };
    }];
    virtualisation.fileSystems."/boot/firmware" = {
      device = "/dev/disk/by-label/FIRMWARE";
      fsType = "vfat";
      options = [ "noatime" "noauto" "x-systemd.automount" "x-systemd.idle-timeout=1min" ];
    };

    # Two simulated radios: wlan0 is the access point, wlan1 the Pi's Wi-Fi.
    boot.kernelModules = [ "mac80211_hwsim" ];
    services.hostapd = {
      enable = true;
      radios.wlan0 = {
        band = "2g";
        channel = 6;
        countryCode = "US";
        # Plain WPA2-Personal, what most home routers offer.
        networks.wlan0 = {
          inherit ssid;
          authentication = {
            mode = "wpa2-sha1";
            wpaPassword = psk;
          };
        };
      };
    };
    networking.networkmanager.unmanaged = [ "interface-name:wlan0" ];
    networking.interfaces.wlan0.ipv4.addresses = [{ address = "192.168.77.1"; prefixLength = 24; }];
    # DHCP only; port 0 leaves DNS to systemd-resolved.
    services.dnsmasq = {
      enable = true;
      resolveLocalQueries = false;
      settings = {
        port = 0;
        interface = "wlan0";
        bind-dynamic = true;
        dhcp-range = "192.168.77.10,192.168.77.50,1h";
      };
    };
  };

  testScript = ''
    machine.start()
    machine.wait_for_unit("rpi-firstboot.service")

    with subtest("the first boot applied the card's settings"):
        machine.succeed("test -e /var/lib/rpi-firstboot.done")
        machine.succeed('test "$(cat /etc/hostname)" = ${hostName}')
        machine.succeed('test "$(hostnamectl --static)" = ${hostName}')
        for home in ["/root", "/home/nixos"]:
            machine.succeed(f"diff {home}/.ssh/authorized_keys ${sshKey}/id_ed25519.pub")
        # The Wi-Fi passphrase does not stay on the card.
        machine.fail("test -e /boot/firmware/firstboot/wifi")

    with subtest("NetworkManager alone manages the network"):
        machine.succeed("systemctl is-active NetworkManager.service")
        machine.succeed("systemctl is-active wpa_supplicant.service")
        machine.fail("systemctl is-active iwd.service")
        machine.fail("systemctl is-active systemd-networkd.service")
        machine.wait_until_succeeds(
            "nmcli -t -f GENERAL.STATE device show wlan1 | grep -q '(connected)'",
            timeout=120,
        )
        machine.wait_until_succeeds("ip -4 -br addr show wlan1 | grep -q 192.168.77.", timeout=60)
        # Kept in /etc, so the connection outlives switching to a host.
        machine.succeed("test -s /etc/NetworkManager/system-connections/firstboot-wifi.nmconnection")
        machine.succeed("resolvectl status wlan1 | grep -q '+mDNS'")

    with subtest("the owner's key logs in as nixos on the Wi-Fi address"):
        ip = machine.succeed(
            "ip -4 -o addr show wlan1 | awk '{print $4}' | cut -d/ -f1"
        ).strip()
        machine.succeed("install -m 600 ${sshKey}/id_ed25519 /tmp/key")
        machine.succeed(
            "ssh -i /tmp/key -o BatchMode=yes -o StrictHostKeyChecking=no"
            f" nixos@{ip} true"
        )
  '';
}
