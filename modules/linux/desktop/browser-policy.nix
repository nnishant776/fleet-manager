# One set of browser policies, conf.desktop.browserPolicies, enforced in both
# Chromium-based browsers the desktop installs. Chromium reads them from
# /etc/chromium (programs.chromium); Brave reads its own directory, which the
# NixOS module does not write, so the same policies go there too. A policy
# set here cannot be changed in the browser's settings.
{ config, lib, ... }:
let
  cfg = config.conf.desktop;
  policies = lib.filterAttrs (_: v: v != null) cfg.browserPolicies;
in
{
  config = lib.mkIf cfg.enable {
    conf.desktop.browserPolicies = lib.mapAttrs (_: lib.mkDefault) {
      # Safe Browsing stays on. The standard level checks against Google's
      # lists; the enhanced one would send more of what is visited.
      SafeBrowsingProtectionLevel = 1;
      # Card numbers are not kept in the browser profile.
      AutofillCreditCardEnabled = false;
    };

    programs.chromium.extraOpts = policies;
    environment.etc."brave/policies/managed/fleet.json".text = builtins.toJSON policies;
  };
}
