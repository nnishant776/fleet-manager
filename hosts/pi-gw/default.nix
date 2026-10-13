# Raspberry Pi 5 that hosts the home network's VMs (guests.nix):
#   public  public services, reachable only through a Cloudflare Tunnel
#   wg      the home router (Wi-Fi AP and wired LAN), WireGuard endpoint and
#           private services
# The Pi itself runs no services: it is a Wi-Fi client of the ISP router and
# routes, NATs and filters between that uplink and the VMs (network.nix).
#
# This file only takes { config, pkgs, lib } so flake.nix can read the platform
# from it; the board support, which needs `inputs`, is in hardware.nix.
{ config, pkgs, lib, ... }: {
  nixpkgs.hostPlatform = "aarch64-linux";

  conf = {
    role = "minimal";
    machineType = "headless";

    host.name = "pi-gw";

    users.accounts.admin = {
      fullName = "Administrator";
      email = "admin@example.com";
      privileged = true;
      initialHashedPassword = "$y$j9T$Em3GOBdeSlR5rvnBakCQt1$MNH7/4KvTt423qqDDHsSUAz96SCUWm5AKMqjy5hzFS3";
      # Password login over SSH is off; without a key the Pi is reachable from
      # its console only.
      sshKeys = [
        # "ssh-ed25519 AAAA... admin@generic"
      ];
    };

    sharing = {
      enable = true;
      ssh = {
        enable = true;
        # Opened on the wg VM's link only, in network.nix.
        config.openFirewall = false;
      };
    };
  };
}
