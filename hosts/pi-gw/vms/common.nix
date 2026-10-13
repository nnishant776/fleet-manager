# Base for every VM on pi-gw. Guests are plain NixOS rather than repo hosts:
# no conf.* tree, no users beyond what a VM's own file adds, and no shell
# login on the console.
{ lib, ... }:
let
  a = import ./addresses.nix;
in {
  imports = [ ./host-user.nix ];

  system.stateVersion = "26.05";

  microvm.hypervisor = "qemu";

  # systemd reads the credentials QEMU passes through fw_cfg in early boot.
  boot.initrd.availableKernelModules = [ "qemu_fw_cfg" ];
  boot.initrd.kernelModules = [ "qemu_fw_cfg" ];

  networking.useNetworkd = true;
  networking.useDHCP = false;
  # Fixed upstream resolvers; services resolve through resolved on 127.0.0.53,
  # which units barred from private ranges can still reach.
  services.resolved = {
    enable = true;
    settings.Resolve = {
      DNS = a.upstreamDns;
      DNSOverTLS = "opportunistic";
      DNSSEC = "allow-downgrade";
      FallbackDNS = [ ];
    };
  };

  # A VM is replaced from the host, never logged into to fix; root is locked.
  users.mutableUsers = false;
  users.users.root.hashedPassword = "!";
  users.allowNoPasswordLogin = true;

  boot.kernel.sysctl = {
    "kernel.dmesg_restrict" = 1;
    "kernel.kptr_restrict" = 2;
    "kernel.unprivileged_bpf_disabled" = 1;
    "net.core.bpf_jit_harden" = 2;
    "kernel.yama.ptrace_scope" = 1;
  };

  nix.enable = false;
  documentation.enable = false;
}
