{ ... }: {
  imports = [
    ./sync.nix
    ./support.nix
    ./home-drift.nix
    ./bootstrap.nix
    ./secrets-sync.nix
  ];
}
