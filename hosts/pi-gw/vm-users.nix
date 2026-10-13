# One host user per VM (design doc, N1). microvm.nix runs every VM's QEMU as
# one shared user, `microvm` in group `kvm`, which can open every VM's state
# directory, taps and macvtap devices: a QEMU escape from the public VM would
# reach the wg VM's disk (the WireGuard key) and, through its macvtap, the home
# LAN. Here each VM's QEMU runs as guest-<vm> in a group of its own, and no
# VM shares a group with another. /dev/kvm, /dev/net/tun and /dev/vhost-net are
# world-accessible on NixOS, so nothing needs `kvm`.
#
# microvm.nix hardcodes microvm:kvm. The overrides are:
#   - the microvm@ and microvm-set-booted@ units' User and Group, below
#   - the state directories' tmpfiles rules, below
#   - the tap and macvtap set-up scripts in each runner, vms/host-user.nix
# A microvm.nix update can undo one; tests/pi-gw/vm-users.nix checks them.
#
# On top of that, each microvm@ unit is confined to its own state directory
# and to the devices its VM uses.
{ config, lib, ... }:
let
  stateDir = config.microvm.stateDir;
  names = lib.attrNames config.microvm.vms;
  inherit (import ./vms/names.nix) userOf credentialsDir;
  forEach = f: lib.mkMerge (map f names);

  guestOf = name: config.microvm.vms.${name}.config.config;
  usesUsb = name: lib.any (d: d.bus == "usb") (guestOf name).microvm.devices;

  # What the VM's QEMU opens besides the pseudo-devices (null, urandom, ...)
  # that DevicePolicy=closed always allows.
  devicesOf = name:
    let
      interfaces = (guestOf name).microvm.interfaces;
    in
    [ "/dev/kvm rw" "/dev/net/tun rw" ]
    ++ lib.optional (lib.any (i: i.type == "tap" && i.tap.vhost) interfaces) "/dev/vhost-net rw"
    ++ lib.optional (lib.any (i: i.type == "macvtap") interfaces) "char-macvtap rw"
    ++ lib.optional (usesUsb name) "char-usb_device rw";
in {
  assertions = map (name: {
    assertion = (guestOf name).networking.hostName == name;
    message = "guest ${name}: its hostName must be its name; vms/host-user.nix derives the host user from it";
  }) names;

  users.users = forEach (name: {
    ${userOf name} = {
      isSystemUser = true;
      group = userOf name;
    };
  });
  users.groups = forEach (name: { ${userOf name} = { }; });

  systemd.tmpfiles.settings = {
    "10-microvm" = lib.mkMerge [
      # Only root (installs, the microvm command) writes here; the shared
      # `microvm` user has nothing left to do.
      {
        ${stateDir}.d = {
          user = lib.mkForce "root";
          group = lib.mkForce "root";
          mode = lib.mkForce "0755";
        };
      }
      (forEach (name: {
        "${stateDir}/${name}".d = {
          user = lib.mkForce (userOf name);
          group = lib.mkForce (userOf name);
          mode = lib.mkForce "0700";
        };
      }))
    ];

    # Files QEMU passes to a VM as systemd credentials (microvm.credentialFiles),
    # readable by that VM's user only.
    "20-guest-credentials" = lib.mkMerge [
      { "${credentialsDir}".d = { user = "root"; group = "root"; mode = "0755"; }; }
      (forEach (name: {
        "${credentialsDir}/${name}".d = { user = "root"; group = userOf name; mode = "0750"; };
      }))
    ];
  };

  systemd.services = forEach (name: {
    # microvm.nix already makes this instance a drop-in on its template.
    "microvm@${name}".serviceConfig = {
      User = userOf name;
      Group = userOf name;

      ProtectSystem = "strict";
      ReadWritePaths = [ "${stateDir}/${name}" ];
      ProtectHome = true;
      PrivateTmp = true;
      DevicePolicy = "closed";
      DeviceAllow = devicesOf name;
      # QMP and sd_notify; libusb watches for USB devices over netlink.
      RestrictAddressFamilies = [ "AF_UNIX" ] ++ lib.optional (usesUsb name) "AF_NETLINK";
      NoNewPrivileges = true;
      CapabilityBoundingSet = "";
      ProtectClock = true;
      ProtectControlGroups = true;
      ProtectHostname = true;
      ProtectKernelLogs = true;
      ProtectKernelModules = true;
      ProtectKernelTunables = true;
      LockPersonality = true;
      RestrictNamespaces = true;
      RestrictRealtime = true;
      RestrictSUIDSGID = true;
      SystemCallArchitectures = "native";
      UMask = "0077";
    };

    # Writes the `booted` link in the VM's state directory.
    "microvm-set-booted@${name}" = {
      overrideStrategy = "asDropin";
      serviceConfig = {
        User = userOf name;
        Group = userOf name;
      };
    };
  });
}
