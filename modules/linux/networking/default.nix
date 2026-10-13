{ ... }: {
  imports = [
    ./backend.nix
    ./networkd.nix
    ./firewall.nix
    ./dns.nix
    ./bluetooth.nix
  ];
}
