# What happened on this machine, for a reviewer who was not there. Rules are
# kept few and specific; a rule on every root exec would drown the log in
# service starts.
{ config, lib, ... }:
{
  config = {
    security.auditd.enable = lib.mkDefault true;
    # true, not "lock": locked rules cannot be changed until reboot, and the
    # sync has to be able to apply a new rule set on activation.
    security.audit.enable = lib.mkDefault true;
    security.audit.rules = [
      # Commands run as root by a logged-in person (sudo), not by services.
      "-a exit,always -F arch=b64 -F euid=0 -F auid>=1000 -F auid!=4294967295 -S execve -k root-exec"
      # The configuration and the generations built from it.
      "-w ${config.conf.fleet.localPath} -p wa -k fleet-manager"
      "-w /nix/var/nix/profiles -p wa -k nix-profiles"
      # Identity and privilege.
      "-w /etc/passwd -p wa -k identity"
      "-w /etc/shadow -p wa -k identity"
      "-w /etc/group -p wa -k identity"
      "-w /etc/sudoers -p wa -k sudo"
      "-w /etc/ssh -p wa -k sshd"
    ]
    # Flatpak installs are audited rather than blocked.
    ++ lib.optional config.conf.flatpak.enable "-w /var/lib/flatpak -p wa -k flatpak";
  };
}
