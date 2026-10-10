# The first-boot step of the Raspberry Pi image: apply the per-card settings
# dropped on the FAT firmware partition after flashing. A separate file so it
# can be built (and shellchecked) for any platform.
{ pkgs }:
pkgs.writeShellApplication {
  name = "rpi-firstboot";
  runtimeInputs = with pkgs; [ coreutils gnused systemd networkmanager ];
  text = ''
    dir=/boot/firmware/firstboot

    # Files may have been written on Windows: strip carriage returns.
    read_file() { tr -d '\r' <"$1"; }

    # hostname: the given one, or rpi-<last 4 of the board serial>, so several
    # freshly flashed Pis are still told apart on the network.
    name=""
    [ -s "$dir/hostname" ] && name="$(read_file "$dir/hostname" | head -1 | tr -cd 'a-zA-Z0-9-')"
    if [ -z "$name" ]; then
      serial="$(tr -d '\0' </sys/firmware/devicetree/base/serial-number 2>/dev/null || true)"
      name="rpi-''${serial: -4}"
    fi
    hostnamectl set-hostname "$name"
    echo "rpi-firstboot: hostname $name"

    # authorized_keys: SSH keys for root and the image's nixos user.
    if [ -s "$dir/authorized_keys" ]; then
      for home in /root /home/nixos; do
        [ -d "$home" ] || continue
        install -d -m 700 "$home/.ssh"
        read_file "$dir/authorized_keys" >>"$home/.ssh/authorized_keys"
        chmod 600 "$home/.ssh/authorized_keys"
        chown -R "$(stat -c %U:%G "$home")" "$home/.ssh"
      done
      echo "rpi-firstboot: installed $(grep -c . "$dir/authorized_keys") SSH key(s)"
    fi

    # wifi: SSID=... and PSK=... lines.
    if [ -s "$dir/wifi" ]; then
      ssid="$(read_file "$dir/wifi" | sed -n 's/^SSID=//p' | head -1)"
      psk="$(read_file "$dir/wifi" | sed -n 's/^PSK=//p' | head -1)"
      if [ -n "$ssid" ]; then
        nmcli connection add type wifi con-name firstboot-wifi ssid "$ssid" \
          wifi-sec.key-mgmt wpa-psk wifi-sec.psk "$psk" connection.autoconnect yes >/dev/null
        echo "rpi-firstboot: Wi-Fi network '$ssid' added"
      fi
      # The passphrase is not left readable on a card that might change hands.
      rm -f "$dir/wifi"
    fi

    # mDNS announces the new name.
    systemctl restart systemd-resolved.service || true
  '';
}
