# Keeps the AAAA record WireGuard clients connect to on the Pi's stable IPv6
# address on the uplink, across ISP prefix changes. The temporary (privacy)
# addresses rotate and are skipped.
#
# The name is its own zone at deSEC (desec.io), delegated from the Cloudflare
# zone by NS records, so nothing on the Pi can change the rest of the domain
# (design doc, H3). The record is written through deSEC's REST API, which
# touches the zone apex's AAAA record only; A is left alone. Give the token a
# default policy that denies everything, then one allowing just that record:
#   {"domain": null, "subname": null, "type": null}
#   {"domain": "private.geeky-panda.dev", "subname": "", "type": "AAAA", "perm_write": true}
# deSEC's docs do not say how a policy names the apex; "" is what its code
# matches, so check that an update still succeeds after adding the policy.
# The TTL is the domain's minimum_ttl at deSEC (support can lower it), so
# clients may use the old address that long after a prefix change; deSEC's
# dynDNS interface (update.dedyn.io) would set 60 s instead. On pi-gw:
#   install -d -m 0700 /var/lib/ddns
#   install -m 0600 /dev/stdin /var/lib/ddns/desec.token   # paste, Ctrl-D
{ pkgs, lib, ... }:
let
  a = import ./vms/addresses.nix;
  sandbox = import ./vms/sandbox.nix;

  zone = "private.geeky-panda.dev";
  api = "https://desec.io/api/v1/domains/${zone}";

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

      # deSEC rate-limits writes: send one only when the address changed.
      last="$STATE_DIRECTORY/address"
      if [ -f "$last" ] && [ "$(< "$last")" = "$addr" ]; then
        exit 0
      fi

      # The token reaches curl as a header file, never on a command line. On
      # an HTTP error curl fails but still returns deSEC's reply, for the log.
      desec() {
        curl -sS --fail-with-body \
          -H @<(printf 'Authorization: Token %s\n' "$(< "$CREDENTIALS_DIRECTORY/token")") \
          "$@"
      }

      if ! domain=$(desec "${api}/"); then
        echo "deSEC: cannot read ${zone}: $domain" >&2
        exit 1
      fi
      ttl=$(jq -er '.minimum_ttl' <<<"$domain")

      # A bulk PATCH creates the RRset or replaces its records, and leaves
      # every other RRset alone. The zone apex is the empty subname.
      body=$(jq -nc --arg addr "$addr" --argjson ttl "$ttl" \
        '[{subname: "", type: "AAAA", ttl: $ttl, records: [$addr]}]')
      if ! reply=$(desec -X PATCH -H 'Content-Type: application/json' --data "$body" "${api}/rrsets/"); then
        echo "deSEC refused the update: $reply" >&2
        exit 1
      fi

      echo "$addr" > "$last"
      echo "${zone} -> $addr (TTL $ttl)"
    '';
  };
in {
  systemd.services.ddns-aaaa = {
    description = "Point ${zone} at this host's stable IPv6 address";
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
