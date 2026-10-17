{ pkgs, lib, config, ... }:
let
  cfg = config.conf.virtualisation;
  virtPackages = with pkgs; [
    qemu
    qemu_kvm
    libvirt
    virt-manager
    bridge-utils
    dnsmasq
    vde2
    # libvirt's networks follow the nftables firewall; it needs neither
    # iptables nor ebtables for them.
    nftables
    dmidecode
  ] ++ cfg.extraPackages;

in {
  config = lib.mkIf cfg.enable {
    virtualisation.libvirtd = {
      enable = true;
      qemu = {
        package = pkgs.qemu_kvm;
        # QEMU runs as the qemu-libvirtd user; a guest escape is not root.
        runAsRoot = lib.mkDefault false;
        swtpm.enable = true;
        # ovmf.enable = true; // Deprecated
      };
    };

    programs.virt-manager.enable = true;

    # libvirt turns forwarding on for its NAT networks. Forwarding is denied
    # unless allowed (see containers/docker.nix); VMs on libvirt's networks
    # (virbr0, ...) are allowed out.
    networking.firewall.filterForward = true;
    networking.firewall.extraForwardRules = ''
      iifname "virbr*" accept comment "libvirt's networks"
    '';

    environment.systemPackages = virtPackages;

    # Export virtualization runtime libraries to nix-ld
    programs.nix-ld.libraries = lib.mkIf (config.programs.nix-ld.enable or false) (
      virtPackages ++ cfg.nix-ldLibraries
    );
  };
}
