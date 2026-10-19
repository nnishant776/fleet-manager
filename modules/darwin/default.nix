{ ... }: {
  imports = [
    ./host
    ./baseline.nix
    ./homebrew.nix
    ./containers.nix
    ./virtualisation.nix
    ./secrets.nix
  ];
}
