Per-card settings, applied once on first boot. Create any of these files in
this folder after flashing the card; all are optional.

  hostname         one line, e.g. lab-pi-3
                   (default: rpi-<last 4 characters of the board serial>)

  authorized_keys  SSH public keys, one per line, for root and the nixos user

  wifi             two lines:
                     SSID=MyNetwork
                     PSK=the-passphrase
                   deleted after it has been applied

Then boot the Pi, log in over SSH and switch it to its host configuration.
