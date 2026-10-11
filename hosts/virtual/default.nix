{ config, pkgs, lib, ... }:
{
  nixpkgs.hostPlatform = "x86_64-linux";

  conf = {
    role = "workstation";
    machineType = "vm";

    host.name = "virtual";

    users.accounts.admin = {
      fullName = "Administrator";
      email = "admin@example.com";
      privileged = true;
      initialHashedPassword = "$y$j9T$Em3GOBdeSlR5rvnBakCQt1$MNH7/4KvTt423qqDDHsSUAz96SCUWm5AKMqjy5hzFS3";
    };

    hardware = {
      graphics.vendor = "intel";
      boot.mode = "uefi";
      disk = {
        enable = true;
        device = "/dev/vda";
        encrypt = false;
        volumes.swap.size = "4G";
      };
    };

    desktop = {
      environments.hyprland = {
        enable = true;
        shell = "none";
      };
    };

    development = {
      enable = true;
      sdk = {
        base.enable = true;
        cpp.enable = false;
      };
      tools = {
        opencode.enable = true;
      };
    };
  };

  # ------------- Host-specific configuration ---------------
  fonts.fontconfig = {
    enable = true;
    defaultFonts = {
      serif = [ "LiterationSerif Nerd Font" ];
      sansSerif = [ "LiterationSans Nerd Font" ];
      monospace = [ "LiterationMono Nerd Font" ];
    };
    hinting = {
      enable = true;
      style = "full";
    };
    includeUserConf = true;
    subpixel = {
      rgba = "rgb";
    };
  };
}
