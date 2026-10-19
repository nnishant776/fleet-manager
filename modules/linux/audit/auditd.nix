# What happened on this machine, for a reviewer who was not there. Rules are
# kept few and specific; a rule on every root exec would drown the log in
# service starts.
{ config, lib, pkgs, ... }:
let
  # Watched for writes and attribute changes. A file is watched as itself; a
  # directory, everything under it.
  watches = [
    # The configuration and the generations built from it.
    { path = config.conf.fleet.localPath; key = "fleet-manager"; }
    { path = "/nix/var/nix/profiles"; key = "nix-profiles"; }
    # Identity and privilege.
    { path = "/etc/passwd"; key = "identity"; file = true; }
    { path = "/etc/shadow"; key = "identity"; file = true; }
    { path = "/etc/group"; key = "identity"; file = true; }
    { path = "/etc/sudoers"; key = "sudo"; file = true; }
    { path = "/etc/ssh"; key = "sshd"; }
  ]
  # Flatpak installs are audited rather than blocked.
  ++ lib.optional config.conf.flatpak.enable { path = "/var/lib/flatpak"; key = "flatpak"; };

  watchRule = w:
    "-a always,exit -F ${if w.file or false then "path" else "dir"}=${w.path} -F perm=wa -k ${w.key}";

  # What has to exist before the rules load: a watched directory itself
  # (`dir=` refuses one that does not exist yet), and a watched file's
  # parent (`path=` accepts a file that appears later, in a directory that
  # exists). Made empty and root's when missing: the configuration checkout
  # before the first boot's clone, for one, which fills it and loads the
  # rules again (fleet/bootstrap.nix), and /nix/var/nix in a VM.
  needed = lib.unique (map (w: if w.file or false then dirOf w.path else w.path) watches);
in
{
  config = {
    security.auditd.enable = lib.mkDefault true;
    # true, not "lock": locked rules cannot be changed until reboot, and the
    # sync has to be able to apply a new rule set on activation.
    security.audit.enable = lib.mkDefault true;
    security.audit.rules = [
      # Go on past a rule that cannot be added, instead of loading none of
      # them; auditctl still fails, so the unit shows the problem.
      "-c"
      # Commands run as root by a logged-in person (sudo), not by services.
      "-a exit,always -F arch=b64 -F euid=0 -F auid>=1000 -F auid!=4294967295 -S execve -k root-exec"
    ] ++ map watchRule watches;

    systemd.services.audit-rules-nixos.serviceConfig.ExecStartPre =
      "${pkgs.coreutils}/bin/mkdir -p ${lib.escapeShellArgs needed}";
  };
}
