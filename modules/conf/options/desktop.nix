{ lib, ... }:
let
  packageList = desc: lib.mkOption {
    type = lib.types.listOf lib.types.package;
    default = [ ];
    description = desc;
  };
in {
  options.conf.desktop = {
    enable = lib.mkEnableOption "a graphical desktop and the ReGreet login screen";

    environments = {
      gnome.enable = lib.mkEnableOption "the GNOME desktop environment";
      hyprland = {
        enable = lib.mkEnableOption "the Hyprland desktop environment";
        shell = lib.mkOption {
          type = lib.types.enum [ "none" "caelestia" "noctalia" "dms" ];
          default = "none";
          description = "Desktop shell to run on Hyprland, or `none` for a bare compositor.";
        };
      };
      sway.enable = lib.mkEnableOption "the Sway desktop environment";
    };

    greeter.wallpaper = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = lib.literalExpression "./wallpaper.jpg";
      description = ''
        Background image for the ReGreet login screen, scaled to cover the
        screen. The file is copied into the store. ReGreet cannot show user
        avatars, so there is no option for them.
      '';
    };

    packages = packageList "Replaces the curated default set of desktop applications.";
    extraPackages = packageList "Appended to whichever set `packages` resolves to.";

    fonts = {
      packages = packageList "Replaces the curated default set of fonts.";
      extraPackages = packageList "Appended to whichever set `fonts.packages` resolves to.";
    };

    idleLockSeconds = lib.mkOption {
      type = lib.types.int;
      default = 600;
      description = ''
        Seconds of inactivity before the session locks. Enforced through dconf
        on GNOME and through a system-level swayidle unit on Hyprland without a
        shell and on Sway; shells such as DMS carry their own idle handling.
        `0` disables the organisation's locker.
      '';
    };

    browserPolicies = lib.mkOption {
      type = lib.types.attrsOf lib.types.anything;
      default = { };
      example = lib.literalExpression ''
        {
          PasswordManagerEnabled = false;
          ExtensionInstallBlocklist = [ "*" ];
          ExtensionInstallAllowlist = [ "nngceckbapebfimnlniiiahkandclblb" ];
        }
      '';
      description = ''
        Chromium enterprise policies, by name, enforced in Chromium and Brave
        (https://chromeenterprise.google/policies/). The desktop sets two as
        defaults: Safe Browsing on at the standard level, and no card numbers
        kept by autofill. A host overrides one by name, and `null` leaves a
        policy unset.
      '';
    };

    printing.enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Print to printers on the network: CUPS on localhost, driverless
        discovery through cups-browsed and Avahi. This machine is never a print
        server and advertises nothing. On by default wherever the desktop is.
      '';
    };

    multimedia = {
      enable = lib.mkEnableOption "the audio and video stack (codecs, players)";
      extraPackages = packageList "Extra audio and video packages.";
      nix-ldLibraries = packageList "Runtime shared libraries exported to nix-ld for audio and video.";
    };
  };
}
