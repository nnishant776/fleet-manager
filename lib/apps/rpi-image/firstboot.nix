# Added to the Raspberry Pi installer image by the rpi-image app (image.nix);
# not part of any host. Reads the files placed in firstboot/ on the card's FAT
# partition, once, on first boot. Kept free of image-only options so the
# rpi-image-firstboot VM test can boot it.
{ config, lib, pkgs, ... }:
{
  # The installer fixes its hostname, which makes /etc/hostname read-only;
  # left unset, the first-boot service can give each card its own. NixOS also
  # points hostnamed at /etc/static/hostname, inside the read-only store, so
  # `hostnamectl set-hostname` fails even then; aim it at the plain file.
  # Switching to a host later replaces that file with its own hostname.
  networking.hostName = lib.mkForce "";
  systemd.services.systemd-hostnamed.environment.SYSTEMD_ETC_HOSTNAME = lib.mkForce "/etc/hostname";

  # The installer stacks two network setups: NixOS's installation-device
  # profile turns on NetworkManager (with wpa_supplicant), and nixos-images
  # adds systemd-networkd with iwd. Both then fight over the interfaces, and
  # Wi-Fi never comes up. Keep NetworkManager only: the first-boot step adds
  # the Wi-Fi network with nmcli, and the hosts in this flake use
  # NetworkManager too, so the connection survives switching to one.
  networking.networkmanager.enable = true;
  networking.wireless.iwd.enable = lib.mkForce false;
  # NetworkManager drives Wi-Fi through wpa_supplicant, which it gets by
  # turning on networking.wireless; nixos-raspberrypi's installer forces that
  # off (mkForce) to leave Wi-Fi to iwd, which leaves wlan0 "unavailable".
  networking.wireless.enable = lib.mkOverride 40 true;
  networking.useNetworkd = lib.mkForce false;
  systemd.network.enable = lib.mkForce false;
  # mDNS through systemd-resolved, as networkd provided it, so the Pi answers
  # as <hostname>.local. Resolved only came with networkd; it is what the
  # hosts use too (modules/linux/networking/dns.nix).
  services.resolved = {
    enable = true;
    settings.Resolve.MulticastDNS = "yes";
  };
  networking.networkmanager.dns = "systemd-resolved";
  networking.networkmanager.connectionConfig."connection.mdns" = 2;

  # The settings above override what nixos-raspberrypi and nixos-images set,
  # so an update of either could undo them. Checked against the real image by
  # the rpi-image-config check and on every build of the image.
  assertions = [
    {
      assertion = config.networking.networkmanager.enable
        && config.networking.networkmanager.wifi.backend == "wpa_supplicant"
        && config.networking.wireless.enable
        && config.networking.wireless.dbusControlled;
      message = "rpi-image: NetworkManager must drive Wi-Fi through its own wpa_supplicant.";
    }
    {
      assertion = !config.networking.wireless.iwd.enable
        && !config.networking.useNetworkd
        && !config.systemd.network.enable;
      message = "rpi-image: iwd and systemd-networkd must stay off; NetworkManager manages every interface.";
    }
    {
      assertion = config.systemd.services.systemd-hostnamed.environment.SYSTEMD_ETC_HOSTNAME == "/etc/hostname";
      message = "rpi-image: hostnamed must write /etc/hostname, or the first boot cannot set the hostname.";
    }
  ];

  systemd.services.rpi-firstboot = {
    description = "Apply per-card settings from the firmware partition";
    wantedBy = [ "multi-user.target" ];
    after = [ "NetworkManager.service" "systemd-hostnamed.service" ];
    before = [ "sshd.service" ];
    unitConfig = {
      RequiresMountsFor = "/boot/firmware";
      ConditionPathExists = "!/var/lib/rpi-firstboot.done";
    };
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = lib.getExe (import ./firstboot-script.nix { inherit pkgs; });
      ExecStartPost = "${pkgs.coreutils}/bin/touch /var/lib/rpi-firstboot.done";
    };
  };
}
