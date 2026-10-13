# Keeps the AAAA record WireGuard clients connect to on the Pi's stable IPv6
# address on the uplink, across ISP prefix changes. The temporary (privacy)
# addresses rotate and are skipped.
#
# The name is its own zone at deSEC (desec.io), delegated from the Cloudflare
# zone by NS records, so nothing on the Pi can change the rest of the domain
# (design doc, H3). The token's policies should allow writing only this name's
# A and AAAA records (the update interface may touch both; this script keeps A
# as it is). On pi-gw:
#   install -d -m 0700 /var/lib/ddns
#   install -m 0600 /dev/stdin /var/lib/ddns/desec.token   # paste, Ctrl-D
{ pkgs, lib, ... }:
let
  a = import ./vms/addresses.nix;
  sandbox = import ./vms/sandbox.nix;

  hostname = "private.geeky-panda.dev";

  update = pkgs.writeShellApplication {
    name = "ddns-aaaa";
    runtimeInputs = with pkgs; [ curl jq iproute2 ];
    text = ''
      addr=$(ip -j -6 addr show dev ${a.host.uplink} scope global -temporary -deprecated -tentative \
        | jq -r '[.[].addr_info[] | select(.family == "inet6") | .local
                  | select(startswith("fd") or startswith("fc") | not)][0] // empty')
      if [ -z "$addr" ]; then
        echo "no stable global IPv6 address on ${a.host.uplink}" >&2
        exit 1
      fi

      # deSEC rate-limits updates: send one only when the address changed.
      last="$STATE_DIRECTORY/address"
      if [ -f "$last" ] && [ "$(< "$last")" = "$addr" ]; then
        exit 0
      fi

      # The token reaches curl as a header file, never on a command line.
      reply=$(curl -fsS \
        -H @<(printf 'Authorization: Token %s\n' "$(< "$CREDENTIALS_DIRECTORY/token")") \
        "https://update.dedyn.io/?hostname=${hostname}&myipv6=$addr&myipv4=preserve")
      if [ "$reply" != good ]; then
        echo "deSEC refused the update: $reply" >&2
        exit 1
      fi
      echo "$addr" > "$last"
      echo "${hostname} -> $addr"
    '';
  };
in {
  systemd.services.ddns-aaaa = {
    description = "Point ${hostname} at this host's stable IPv6 address";
    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];
    serviceConfig = sandbox.serviceConfig // {
      Type = "oneshot";
      ExecStart = lib.getExe update;
      LoadCredential = [ "token:/var/lib/ddns/desec.token" ];
      StateDirectory = "ddns-aaaa";
      # Reads the uplink's addresses over netlink, talks to deSEC only.
      IPAddressDeny = sandbox.privateRanges;
    };
  };

  systemd.timers.ddns-aaaa = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnBootSec = "1min";
      OnUnitActiveSec = "5min";
    };
  };
}
