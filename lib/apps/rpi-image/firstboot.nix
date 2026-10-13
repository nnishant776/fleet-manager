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

  # The network as on a host with the `networkd` backend
  # (modules/linux/networking/networkd.nix): systemd-networkd gives wired and
  # Wi-Fi interfaces addresses by DHCP, and wpa_supplicant joins the networks
  # kept in /etc/wpa_supplicant/imperative.conf. The first boot writes the
  # card's Wi-Fi network there, and such a host reads the same file, so the
  # connection survives switching to one.
  #
  # The installer stacks other setups that must go: NixOS's installation-device
  # profile turns on NetworkManager, whose module also turns DHCP off, which
  # left Wi-Fi with IPv6 only; nixos-images adds iwd; and nixos-raspberrypi
  # forces networking.wireless off (mkForce) to leave Wi-Fi to iwd.
  networking.networkmanager.enable = lib.mkForce false;
  networking.wireless.iwd.enable = lib.mkForce false;
  networking.wireless = {
    enable = lib.mkOverride 40 true;
    allowAuxiliaryImperativeNetworks = true;
    userControlled = true;
    dbusControlled = false;
  };
  # NixOS' 99-ethernet-default-dhcp and 99-wireless-client-dhcp networks, to
  # which nixos-images adds mDNS.
  networking.useNetworkd = true;
  networking.useDHCP = true;
  systemd.network.enable = true;
  # mDNS through systemd-resolved, so the Pi answers as <hostname>.local.
  services.resolved = {
    enable = true;
    settings.Resolve.MulticastDNS = "yes";
  };

  # The card's passphrase ends up in imperative.conf, but NixOS makes it
  # readable by everyone (0664) each time wpa_supplicant starts. Closing the
  # directory keeps it private at all times; the file mode is tightened once
  # started. As on a networkd host.
  systemd.tmpfiles.rules = [ "d /etc/wpa_supplicant 0750 wpa_supplicant wpa_supplicant -" ];
  systemd.services.wpa_supplicant.serviceConfig.ExecStartPre = lib.mkAfter [
    "+${pkgs.coreutils}/bin/chmod 0660 /etc/wpa_supplicant/imperative.conf"
  ];

  # The settings above override what nixos-raspberrypi and nixos-images set,
  # so an update of either could undo them. Checked against the real image by
  # the rpi-image-config check and on every build of the image.
  assertions = [
    {
      assertion = !config.networking.networkmanager.enable && !config.networking.wireless.iwd.enable;
      message = "rpi-image: NetworkManager and iwd must stay off; systemd-networkd and wpa_supplicant manage the network.";
    }
    {
      assertion = config.networking.wireless.enable
        && config.networking.wireless.allowAuxiliaryImperativeNetworks
        && config.networking.wireless.interfaces == [ ];
      message = "rpi-image: wpa_supplicant must run on every Wi-Fi interface and read /etc/wpa_supplicant/imperative.conf.";
    }
    {
      assertion = config.systemd.network.enable
        && (config.systemd.network.networks."99-wireless-client-dhcp".networkConfig.DHCP or null) == "yes"
        && (config.systemd.network.networks."99-ethernet-default-dhcp".networkConfig.DHCP or null) == "yes";
      message = "rpi-image: systemd-networkd must give wired and Wi-Fi interfaces addresses by DHCP, IPv4 included.";
    }
    {
      assertion = config.systemd.services.systemd-hostnamed.environment.SYSTEMD_ETC_HOSTNAME == "/etc/hostname";
      message = "rpi-image: hostnamed must write /etc/hostname, or the first boot cannot set the hostname.";
    }
  ];

  systemd.services.rpi-firstboot = {
    description = "Apply per-card settings from the firmware partition";
    wantedBy = [ "multi-user.target" ];
    # Before wpa_supplicant reads the networks, and before sshd reads the keys.
    before = [ "wpa_supplicant.service" "sshd.service" ];
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
