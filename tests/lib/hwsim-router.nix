# A Wi-Fi "router" for VM tests. It moves one mac80211_hwsim radio into its
# own network namespace and runs an access point with DHCP there, so the
# machine under test reaches it only over the simulated air, as it would a
# real router. Offers WPA2 and WPA3 at once, like most home routers.
#
#   router = import ../lib/hwsim-router.nix { inherit pkgs; } { ssid = ...; passphrase = ...; };
#   environment.systemPackages = [ router ];   # then run `hwsim-router` in the test
#
# The passphrase must not contain `|`, which hostapd reads as a separator in
# sae_password, nor a newline.
{ pkgs }:
{ ssid
, passphrase
, iface ? "wlan0"
, ns ? "router"
, subnet ? "192.168.77"
, countryCode ? "IN"
, channel ? 6
}:
let
  conf = pkgs.writeText "hwsim-router-hostapd.conf" ''
    interface=${iface}
    driver=nl80211
    ssid=${ssid}
    country_code=${countryCode}
    hw_mode=g
    channel=${toString channel}
    ieee80211n=1
    wmm_enabled=1
    wpa=2
    wpa_key_mgmt=WPA-PSK SAE
    rsn_pairwise=CCMP
    ieee80211w=1
    sae_require_mfp=1
    sae_pwe=2
    wpa_passphrase=${passphrase}
    sae_password=${passphrase}
  '';
in
pkgs.writeShellApplication {
  name = "hwsim-router";
  runtimeInputs = with pkgs; [ iw iproute2 gawk hostapd dnsmasq ];
  text = ''
    phy=$(iw dev ${iface} info | awk '/wiphy/ { print "phy" $2 }')
    ip netns add ${ns}
    iw phy "$phy" set netns name ${ns}
    ip netns exec ${ns} ip link set lo up
    ip netns exec ${ns} ip addr add ${subnet}.1/24 dev ${iface}
    ip netns exec ${ns} hostapd -B -P /run/hwsim-router-hostapd.pid ${conf}
    ip netns exec ${ns} dnsmasq --port=0 --interface=${iface} --bind-dynamic \
      --dhcp-range=${subnet}.10,${subnet}.50,1h \
      --pid-file=/run/hwsim-router-dnsmasq.pid \
      --dhcp-leasefile=/run/hwsim-router-dnsmasq.leases
  '';
}
