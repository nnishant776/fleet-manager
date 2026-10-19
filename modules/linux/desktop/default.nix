{ ... }: {
  imports = [
    ./shells
    ./greeter
    ./portals.nix
    ./audio.nix
    ./services.nix
    ./packages.nix
    ./browser-policy.nix
    ./printing.nix
    ./idle.nix
    ./fonts.nix
  ];
}
