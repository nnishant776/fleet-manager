{ ... }: {
  imports = [
    ./backend.nix
    ./networkd.nix
    ./hotspot.nix
    ./firewall.nix
    ./dns.nix
    ./bluetooth.nix
  ];
}
