# 6. Networking

<!-- book:top — written by `fleet docs` from SUMMARY.md and the headings; do not edit -->
[← 5. Accounts and homes](accounts.md) · [Contents](README.md) · [7. Signing commits and trusting them →](signing-and-trust.md)
<!-- /book:top -->

What `conf.networking` does beyond its options: the firewall and where a
host's own rules go, stable interface names, Wi-Fi networks kept on the
machine with networkd, and hotspots that share the machine's connection. The
options are under
[`conf.networking` and `conf.sharing`](configuration.md#confnetworking-and-confsharing)
in *Configuration reference*.

<!-- book:sections -->
In this chapter:

- [The firewall](#the-firewall)
- [Interface names from the MAC](#interface-names-from-the-mac)
- [Wi-Fi networks with networkd](#wi-fi-networks-with-networkd)
- [Hotspots](#hotspots)
<!-- /book:sections -->

## The firewall

It is nftables on every Linux host. iptables rules
(`extraCommands`) are refused at build time. Docker still uses iptables
underneath, which lands in nftables too; there is nothing to do.

Rules of a host's own go in one of three options:

- `inputRules`: new connections to the machine. They add access; they cannot
  remove what an open port allows.
- `forwardRules`: traffic passing through the machine. Setting any turns on
  "forwarding denied unless allowed".
- `tables`: a whole nftables table, for anything else. A `drop` is final, but
  an `accept` only ends its own chain. So blocking needs a chain at an earlier
  priority.

```nix
# SSH from the home network only
conf.networking.firewall.inputRules = ''
  ip saddr 192.168.1.0/24 tcp dport 22 accept
'';
```

These options are only for one host's additions and exceptions. A module sets
the rules its own feature needs, and a module that uses these options fails
the build. Table names already used by NixOS, Docker, podman, libvirt or
systemd-networkd are refused.

Hosts with containers, VMs or a hotspot deny forwarded traffic unless it is
allowed. Containers and VMs still reach the network and published ports still
work. But the machine no longer routes between its other networks, which NixOS
with Docker would otherwise do.

## Interface names from the MAC

They are off by default. With
`networking.macNames = true`, every physical network interface is named after
its bus and its MAC address, such as `wlu001a2b3c4d5e`. The name is `wl`
(Wi-Fi) or `en` (wired), then a bus letter, then the MAC without colons. The
bus letter is `u` for USB, `p` for PCI, `s` for SDIO (the Pi's built-in Wi-Fi
is on SDIO), `o` for onboard devices the board itself describes (such as the
Pi 5's built-in Ethernet) and `x` for anything else. The name is 15
characters, the most Linux allows.

The same adapter then gets the same name on every boot and in any port. Kernel
names like `wlan0` depend on the order in which adapters appear. The names
change at the next boot. After that, anything in the host's configuration that
names an interface must use the new name. To find a name, run `ip link` once
the adapter is plugged in, or read the MAC printed on the adapter.

Virtual interfaces (bridges, VLANs, VPNs, container networks), interfaces a
program names itself, and adapters without a fixed MAC keep their names. This
needs the systemd initrd, which is the default here.

## Wi-Fi networks with networkd

They are kept on the machine. With no
interfaces named, one client serves every Wi-Fi interface, and the networks are
in `/etc/wpa_supplicant/imperative.conf`. On a Raspberry Pi, the network from the
card's `firstboot/wifi` file ends up there. To move to a new access point, add
it while the old one still works, then remove the old one:

```bash
wpa_cli add_network                              # prints a number, say 1
wpa_cli set_network 1 ssid '"New network"'
wpa_cli set_network 1 psk '"its password"'
wpa_cli enable_network 1
wpa_cli save_config
# later, once the machine uses the new network:
wpa_cli list_networks                            # find the old one's number
wpa_cli remove_network 0
wpa_cli save_config
```

With `networking.wireless.wifi.interfaces` naming interfaces, each one has its
own file, `/etc/wpa_supplicant/<name>.conf`. The first time, it is a copy of
`imperative.conf`, so a machine on Wi-Fi stays online. Then remove the networks
that interface should not join. `wpa_cli` takes `-i <name>`, and `save_config`
writes only that interface's file:

```bash
wpa_cli -i <name> list_networks
wpa_cli -i <name> remove_network 0
wpa_cli -i <name> save_config
```

An adapter plugged in later starts its client by itself.

## Hotspots

They are named by their adapter's interface name. Turn on
`networking.macNames` so the name is stable, plug the adapter in and read its
name with `ip link`. Whenever there is a hotspot and the Wi-Fi client is on
(`wifi.enable`), name the Wi-Fi client's interfaces in `networking.wireless.wifi.interfaces`, so the client never takes a
hotspot's adapter:

```nix
conf = {
  networking.backend = "networkd";
  networking.macNames = true;
  networking.wireless = {
    countryCode = "IN";
    wifi.enable = true;
    wifi.interfaces.wls001a2b3c4d5e = { };
    hotspots.wlu00c0ca9c8d7e.enable = true;
  };
};
```

Put the hotspot's password in `/var/lib/hotspot/<name>`, as root:

```bash
sudo install -d -m 700 /var/lib/hotspot
sudo sh -c 'umask 077; printf "%s\n" "the password" > /var/lib/hotspot/<name>'
```

Each hotspot has its own service, `hostapd-<name>`. It starts when its adapter
appears and stops when it goes. The other hotspots carry on.
`systemctl restart hostapd-<name>` applies a new password to that one. Devices
on the same hotspot see each other. Each hotspot shares the uplink through NAT.
Devices on one hotspot cannot reach devices on another, in either direction.
They still reach the internet through the uplink. Devices on the uplink side
cannot start connections to hotspot devices. Devices on a hotspot reach this
machine itself only for DHCP, unless `hostAccess = true` for that hotspot (for
your own hotspot, to reach SSH, say). Devices on the uplink's own network, such
as a home LAN, stay reachable from the hotspots, as the internet is reached
through it.

Each hotspot uses the widest channel its adapter and the country allow. That is
usually 80 MHz on 5 GHz, and up to 160 or 320 MHz on 6 GHz, depending on the
adapter. 2.4 GHz stays at 20 MHz. The country is set first. If hostapd refuses
a width, the next narrower one is tried. The choice is in the service's log:
`journalctl -u hostapd-<name>`. `width = 80` (or any other number) pins it.
Radar channels, which 160 MHz on 5 GHz needs, are off unless
`radarChannels = true`. The hotspot then waits a minute or more before it
starts, and many USB adapters cannot do it. In India, 160 MHz on 5 GHz is only
on channels 100 to 128.

For other hostapd settings, use `settings` and `extraConfig`. They are added
last, so they win. Do not set `interface` or `ctrl_interface`. Do not put
secrets in them, as they end up in the Nix store.

`nix flake check` checks every host that has a declared hotspot that is off
(`<host>-hotspot-ready`), so turning it on stays a one-line change.

<!-- book:bottom -->
---

[← 5. Accounts and homes](accounts.md) · [Contents](README.md) · [7. Signing commits and trusting them →](signing-and-trust.md)
<!-- /book:bottom -->
