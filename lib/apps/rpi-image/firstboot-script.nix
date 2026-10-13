# The first-boot step of the Raspberry Pi image: apply the per-card settings
# dropped on the FAT firmware partition after flashing. A separate file so it
# can be built (and shellchecked) for any platform.
{ pkgs }:
pkgs.writeShellApplication {
  name = "rpi-firstboot";
  runtimeInputs = with pkgs; [ coreutils gnused gnugrep systemd ];
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

    # authorized_keys: SSH keys for root and the image's nixos user. Only keys
    # not there yet, so a repeated run adds nothing twice.
    if [ -s "$dir/authorized_keys" ]; then
      for home in /root /home/nixos; do
        [ -d "$home" ] || continue
        install -d -m 700 "$home/.ssh"
        touch "$home/.ssh/authorized_keys"
        read_file "$dir/authorized_keys" | while IFS= read -r key; do
          [ -n "$key" ] || continue
          grep -qxF -- "$key" "$home/.ssh/authorized_keys" || printf '%s\n' "$key" >>"$home/.ssh/authorized_keys"
        done
        chmod 600 "$home/.ssh/authorized_keys"
        chown -R "$(stat -c %U:%G "$home")" "$home/.ssh"
      done
      echo "rpi-firstboot: installed $(grep -c . "$dir/authorized_keys") SSH key(s)"
    fi

    # wifi: SSID=... and PSK=... lines, added as a network to the file
    # wpa_supplicant reads, where a host on the networkd backend finds it too.
    # The SSID is written in hex, so any name works; WPA2 and WPA3 are both
    # allowed, as most routers offer either. No PSK means an open network.
    wifi_error=""
    if [ -s "$dir/wifi" ]; then
      ssid="$(read_file "$dir/wifi" | sed -n 's/^SSID=//p' | head -1)"
      psk="$(read_file "$dir/wifi" | sed -n 's/^PSK=//p' | head -1)"
      if [ -z "$ssid" ]; then
        wifi_error="the wifi file has no SSID= line"
      elif [ -n "$psk" ] && { [ ''${#psk} -lt 8 ] || [ ''${#psk} -gt 63 ]; }; then
        wifi_error="the Wi-Fi password must be 8 to 63 characters"
      else
        # The directory is closed (0750) at boot, by systemd-tmpfiles.
        conf=/etc/wpa_supplicant/imperative.conf
        [ -d /etc/wpa_supplicant ] || install -d -m 750 /etc/wpa_supplicant
        touch "$conf"
        chmod 660 "$conf"
        chown wpa_supplicant:wpa_supplicant "$conf"
        {
          printf 'network={\n'
          printf '\tssid=%s\n' "$(printf '%s' "$ssid" | od -An -v -tx1 | tr -d ' \n')"
          if [ -n "$psk" ]; then
            printf '\tpsk="%s"\n' "$psk"
            printf '\tkey_mgmt=WPA-PSK WPA-PSK-SHA256 SAE\n'
            printf '\tieee80211w=1\n'
          else
            printf '\tkey_mgmt=NONE\n'
          fi
          printf '}\n'
        } >>"$conf"
        echo "rpi-firstboot: Wi-Fi network '$ssid' added"
      fi
      # The passphrase is not left readable on a card that might change hands,
      # whether or not it could be used.
      rm -f "$dir/wifi"
    fi

    # mDNS announces the new name.
    systemctl restart systemd-resolved.service || true

    # Fail visibly after the other settings are applied, so the unit shows as
    # failed and the reason is in its log.
    if [ -n "$wifi_error" ]; then
      echo "rpi-firstboot: Wi-Fi not added: $wifi_error" >&2
      exit 1
    fi
  '';
}
