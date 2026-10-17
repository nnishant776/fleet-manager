{ ... }: {
  imports = [
    ./backend.nix
    ./networkd.nix
    ./hotspot.nix
    ./firewall.nix
    ./names.nix
    ./dns.nix
    ./bluetooth.nix
  ];
}
