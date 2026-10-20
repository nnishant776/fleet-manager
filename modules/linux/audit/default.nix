{ ... }: {
  imports = [
    ./auditd.nix
    ./journald.nix
    ./loki.nix
  ];
}
