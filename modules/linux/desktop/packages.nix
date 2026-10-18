{ pkgs, lib, config, ... }:
let
  defaultDesktopPackages = with pkgs; [
    # Terminal
    alacritty

    # Clipboard
    wl-clipboard

    # Hardware control
    brightnessctl
    libinput

    # Media Controls
    playerctl

    # Networking and Hardware
    blueman
    pavucontrol

    # Internet Browsers
    widevine-cdm
    # With Widevine, nixpkgs runs a full copy of Chromium that has the CDM
    # added, but the wrapper links its share/ entries (the menu file and
    # icons) from the plain build, which keeps a second ~700 MiB copy in the
    # closure. Point those links at the copy the wrapper runs.
    ((chromium.override {
      enableWideVine = true;
      commandLineArgs = [
        "--ignore-gpu-blocklist"
        "--enable-zero-copy"
        "--ozone-platform-hint=auto"
        "--enable-features=WebRTCPipeWireCapturer,VaapiIgnoreDriverChecks,VaapiVideoDecoder,PlatformHEVCDecoderSupport,UseMultiPlaneFormatForHardwareVideo,AcceleratedVideoEncoder"
      ];
    }).overrideAttrs (old: {
      buildCommand = old.buildCommand + ''
        wv="$(grep -o -m 1 '/nix/store/[a-z0-9]*-chromium-unwrapped-[^/]*-wv' "$out/bin/chromium" | head -n 1)"
        [ -n "$wv" ] || { echo "no Widevine copy referenced by $out/bin/chromium" >&2; exit 1; }
        for f in "$out"/share/*; do
          ln -sfn "$wv/share/$(basename "$f")" "$f"
        done
      '';
    }))
    brave

    # File browsers
    thunar

    # Curosr
    phinger-cursors
  ];
in {
  config = lib.mkIf config.conf.desktop.enable {
    # Install desktop packages
    environment.systemPackages = (
      if config.conf.desktop.packages != [] then
        config.conf.desktop.packages
      else
        defaultDesktopPackages
    )
    ++ config.conf.desktop.extraPackages;

    environment.sessionVariables = {
      XCURSOR_THEME = "phinger-cursors-dark"; # or "phinger-cursors-light"
      XCURSOR_SIZE = "24";
    };

    # Configure chromium
    programs.chromium.enable = true;
  };
}
