# Organisation baseline for every managed user.
#
# Values here use lib.mkDefault so per-user organisation config
# (users/<name>/, homeConfig) and, where permitted, the user's own config
# can override them instead of colliding.
{ config, pkgs, lib, ... }: {
  imports = [
    ./packages.nix
    ./shell.nix
    ./git.nix
    ./ssh.nix
    ./revision.nix
  ];

  news.display = lib.mkDefault "silent";

  home = {
    stateVersion = lib.mkDefault "26.05";
    enableNixpkgsReleaseCheck = lib.mkDefault false;
  };

  programs.home-manager.enable = lib.mkDefault true;

  # The system's weekly garbage collection expires the profiles under
  # /nix/var/nix/profiles, but not a user's own home-manager generations,
  # which otherwise keep every old home in the store. Expire them on the
  # same terms: weekly, older than 30 days.
  services.home-manager.autoExpire = {
    enable = lib.mkDefault true;
    timestamp = lib.mkDefault "-30 days";
    frequency = lib.mkDefault "weekly";
  };
}
