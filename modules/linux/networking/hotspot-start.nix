# The start of a hotspot's service (hotspot.nix): the country first, then the
# widest channel width the adapter and the channels around the chosen one
# allow, then hostapd. hostapd checks the width against the kernel's limits
# for each channel, which combine the country's rules and the adapter's;
# when it refuses, the next narrower width is tried.
{ pkgs, lib, name, h, channel, customFile, countryCode }:
let
  ch = toString channel;
  bandNo = { "2g" = "1"; "5g" = "2"; "6g" = "4"; }.${h.band};
  # Widest first. "max" stays at 20 MHz on 2.4 GHz.
  ladder = { "2g" = [ 20 ]; "5g" = [ 160 80 40 20 ]; "6g" = [ 320 160 80 40 20 ]; }.${h.band};
  candidates = if h.width == "max" then ladder else [ h.width ];
in
pkgs.writeShellScript "hostapd-${name}-start" ''
  set -u
  PATH=${lib.makeBinPath [ pkgs.iw pkgs.gawk pkgs.gnugrep pkgs.coreutils pkgs.hostapd ]}
  dir=/run/hostapd-${name}
  log() { echo "${name}: $*"; }

  # The country first: until it is set, the kernel applies the strict
  # worldwide rules (no access point on 5 GHz, nothing on 6 GHz).
  iw reg set ${countryCode}
  for _ in $(seq 50); do
    if iw reg get | grep -q '^country ${countryCode}:'; then break; fi
    sleep 0.1
  done

  phy=$(cat /sys/class/net/${name}/phy80211/name)
  # This band's part of iw's output; iw numbers 6 GHz as band 4.
  section() { awk '/^[[:space:]]*Band [0-9]+:/ { on = index($0, "Band ${bandNo}:") > 0 } on'; }
  caps=$(iw phy "$phy" info | section)
  chans=$(iw phy "$phy" channels | section)
  has() { printf '%s\n' "$caps" | grep -qF -- "$1"; }

  # What the adapter can do on this band.
  supports() {
    case ${h.band}:$1 in
      *:20) return 0 ;;
      5g:40) has 'HT20/HT40' || has 'HE40/HE80/5GHz' ;;
      5g:80) has 'VHT Capabilities' || has 'HE40/HE80/5GHz' ;;
      5g:160) has 'Supported Channel Width: 160' || has 'HE160/5GHz' ;;
      6g:40 | 6g:80) has 'HE40/HE80/5GHz' ;;
      6g:160) has 'HE160/5GHz' ;;
      6g:320) has '320MHz in 6GHz Supported' ;;
      *) return 1 ;;
    esac
  }

  # The 20 MHz channels of the block of width $1 around the channel. On
  # 5 GHz, blocks count from 36 below channel 149 and from 149 above.
  block() {
    n=$(( $1 / 20 ))
    case ${h.band} in
      5g) if [ ${ch} -lt 149 ]; then base=36; else base=149; fi ;;
      6g) base=1 ;;
      *) base=${ch}; n=1 ;;
    esac
    start=$(( base + (${ch} - base) / (4 * n) * 4 * n ))
    seq "$start" 4 $(( start + 4 * (n - 1) ))
  }
  first() { block "$1" | head -n1; }
  centre() { echo $(( ( $(block "$1" | head -n1) + $(block "$1" | tail -n1) ) / 2 )); }

  # Every channel of the block exists, may start an access point, and needs
  # no radar detection unless radarChannels allows it.
  usable() {
    for c in $(block "$1"); do
      info=$(printf '%s\n' "$chans" | awk -v c="[$c]" '/^\t\* / { on = index($0, c) > 0 } on')
      [ -n "$info" ] || return 1
      case $info in *disabled* | *"No IR"*) return 1 ;; esac
      ${lib.optionalString (!h.radarChannels) ''case $info in *"Radar detection"*) return 1 ;; esac''}
    done
    return 0
  }

  # hostapd's settings for width $1, with the adapter's short guard
  # intervals.
  width_lines() {
    w=$1
    case ${h.band} in
      2g)
        if [ "$w" = 40 ]; then
          if [ ${ch} -le 7 ]; then echo 'ht_capab=[HT40+]'; else echo 'ht_capab=[HT40-]'; fi
        fi
        ;;
      5g)
        ht=""
        if [ "$w" -ge 40 ]; then
          if [ ${ch} -eq "$(first 40)" ]; then ht='[HT40+]'; else ht='[HT40-]'; fi
          if has 'RX HT40 SGI'; then ht="''${ht}[SHORT-GI-40]"; fi
        fi
        if has 'RX HT20 SGI'; then ht="''${ht}[SHORT-GI-20]"; fi
        vht=""
        if has 'short GI (80 MHz)'; then vht="''${vht}[SHORT-GI-80]"; fi
        if [ "$w" = 160 ]; then
          vht="''${vht}[VHT160]"
          if has 'short GI (160/80+80 MHz)'; then vht="''${vht}[SHORT-GI-160]"; fi
        fi
        case $w in 80) cw=1 ;; 160) cw=2 ;; *) cw=0 ;; esac
        echo "ht_capab=$ht"
        echo "vht_capab=$vht"
        echo "vht_oper_chwidth=$cw"
        echo "he_oper_chwidth=$cw"
        if [ "$w" -ge 80 ]; then
          echo "vht_oper_centr_freq_seg0_idx=$(centre "$w")"
          echo "he_oper_centr_freq_seg0_idx=$(centre "$w")"
        fi
        ;;
      6g)
        case $w in 20) oc=131 ;; 40) oc=132 ;; 80) oc=133 ;; 160) oc=134 ;; *) oc=137 ;; esac
        case $w in 80) cw=1 ;; 160 | 320) cw=2 ;; *) cw=0 ;; esac
        echo "op_class=$oc"
        echo "he_oper_chwidth=$cw"
        if [ "$w" = 320 ]; then
          # 802.11ax uses the 160 MHz half that holds the channel.
          echo "he_oper_centr_freq_seg0_idx=$(centre 160)"
          echo 'ieee80211be=1'
          echo 'eht_oper_chwidth=9'
          echo "eht_oper_centr_freq_seg0_idx=$(centre 320)"
        else
          echo "he_oper_centr_freq_seg0_idx=$(centre "$w")"
        fi
        ;;
    esac
  }

  for w in ${lib.concatMapStringsSep " " toString candidates}; do
    ${lib.optionalString (h.width == "max") ''
    supports "$w" || continue
    if ! usable "$w"; then
      log "$w MHz: a channel around ${ch} does not allow it"
      continue
    fi
    ''}
    { cat "$dir/base.conf"; width_lines "$w"; cat ${customFile}; } > "$dir/hostapd.conf"
    if hostapd -B -P "$dir/hostapd.pid" "$dir/hostapd.conf"; then
      log "channel ${ch}, $w MHz"
      exit 0
    fi
    log "hostapd refused $w MHz on channel ${ch}"
  done
  log "no channel width worked on channel ${ch}"
  exit 1
''
