{ ... }: {
  imports = [
    ./sync.nix
    ./support.nix
    ./home-drift.nix
    ./bootstrap.nix
    ./secrets-sync.nix
    ./sync-status.nix
    ./sync-now.nix
    ./safe-directory.nix
  ];
}
