# The Mac's own protections that nix-darwin can set, on every Mac in the
# fleet. FileVault, Gatekeeper and SIP belong to MDM and stay there. Each is
# a default that a host can override.
#
# All of it is commented out for now. MDM may manage the same settings, and
# what nix-darwin does on a Mac that MDM manages is to be confirmed on one
# such Mac first. Uncomment a setting once its behaviour there is known.
{ lib, ... }:
{
  # The application firewall: signed software, Apple's and others', may
  # accept connections, and the Mac does not answer probes on ports nothing
  # listens on.
  # networking.applicationFirewall = {
  #   enable = lib.mkDefault true;
  #   enableStealthMode = lib.mkDefault true;
  #   allowSigned = lib.mkDefault true;
  #   allowSignedApp = lib.mkDefault true;
  # };

  # The screen lock is left out: nix-darwin sets it for one user only (it
  # needs system.primaryUser), and a fleet Mac's accounts come from MDM,
  # whose screen saver profile sets it for every account.

  # system.defaults.loginwindow.GuestEnabled = lib.mkDefault false;

  # macOS updates install by themselves, as security fixes do on Linux hosts.
  # system.defaults.SoftwareUpdate.AutomaticallyInstallMacOSUpdates = lib.mkDefault true;

  # Builds see only their declared inputs, as on Linux. Most of what a Mac
  # builds comes from the binary cache anyway.
  # nix.settings.sandbox = lib.mkDefault true;
}
