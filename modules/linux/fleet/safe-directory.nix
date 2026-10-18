# The configuration checkout at conf.fleet.localPath belongs to root, and git
# refuses to work in a repository its user does not own. So does nix, which
# opens flakes with libgit2. Trusting the checkout in /etc/gitconfig lets every
# account use it, including a user's first `nix run /etc/nixos#home-switch`,
# before any home configuration exists that could trust it for them.
{ config, ... }: {
  programs.git = {
    enable = true;
    config.safe.directory = [ config.conf.fleet.localPath ];
  };
}
