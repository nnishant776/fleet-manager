Per-card settings, applied once on first boot. Create any of these files in
this folder after flashing the card; all are optional.

  hostname         one line, e.g. lab-pi-3
                   (default: rpi-<last 4 characters of the board serial>)

  authorized_keys  SSH public keys, one per line, for root and the nixos user

  wifi             two or three lines:
                     SSID=MyNetwork
                     PSK=the-passphrase
                     COUNTRY=IN
                   the passphrase must be 8 to 63 characters; leave out
                   the PSK= line for an open network
                   COUNTRY= is your two-letter country code; without it
                   much of 5 GHz is unusable and the Pi may stay on 2.4 GHz
                   the file is deleted after it has been used

Then boot the Pi, log in over SSH and switch it to its host configuration.
