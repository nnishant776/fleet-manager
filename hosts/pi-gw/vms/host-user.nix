# Taps and macvtap devices owned by this VM's own host user, microvm-<name>
# (design doc, N1; the host side is ../vm-users.nix). microvm.nix creates them
# for its shared `microvm` user, hardcoded in its interfaces.nix; these are its
# set-up scripts with only the owner changed.
{ config, lib, pkgs, ... }:
let
  user = "microvm-${config.networking.hostName}";
  ip = lib.getExe' pkgs.iproute2 "ip";
  ofType = type: builtins.filter (i: i.type == type) config.microvm.interfaces;

  tapFlags = lib.concatStringsSep " " (
    [ "vnet_hdr" ]
    ++ lib.optional config.microvm.declaredRunner.passthru.tapMultiQueue "multi_queue"
  );
in {
  microvm.binScripts.tap-up = lib.mkIf (ofType "tap" != [ ]) (lib.mkForce (''
    set -eou pipefail
  '' + lib.concatMapStrings ({ id, ... }: ''
    if [ -e /sys/class/net/${id} ]; then
      ${ip} link delete '${id}'
    fi

    ${ip} tuntap add name '${id}' mode tap user '${user}' ${tapFlags}
    ${ip} link set '${id}' up
  '') (ofType "tap")));

  microvm.binScripts.macvtap-up = lib.mkIf (ofType "macvtap" != [ ]) (lib.mkForce (''
    set -eou pipefail
  '' + lib.concatMapStrings ({ id, mac, macvtap, ... }: ''
    if [ -e /sys/class/net/${id} ]; then
      ${ip} link delete '${id}'
    fi
    ${ip} link add link '${macvtap.link}' name '${id}' address '${mac}' type macvtap mode '${macvtap.mode}'
    ${ip} link set '${id}' allmulticast on
    if [ -f "/proc/sys/net/ipv6/conf/${id}/disable_ipv6" ]; then
      echo 1 > "/proc/sys/net/ipv6/conf/${id}/disable_ipv6"
    fi
    ${ip} link set '${id}' up
    ${lib.getExe' pkgs.coreutils "chown"} '${user}:${user}' /dev/tap$(< "/sys/class/net/${id}/ifindex")
  '') (ofType "macvtap")));
}
