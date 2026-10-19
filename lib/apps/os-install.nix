# Installs a host from this flake onto a machine booted from installer media.
#
# The live ISO keeps its writable store and / on tmpfs, so anything built or
# downloaded there lives in RAM. Installing therefore happens in two steps that
# keep the system off the installer:
#
#   1. The host's disko script partitions, formats and mounts the disks at
#      /mnt; then the swap the layout declares is turned on, so the evaluation
#      in step 2 can spill to the new disk instead of exhausting RAM.
#   2. nixos-install builds and downloads straight into /mnt/nix/store, with
#      its scratch space on /mnt too, reusing whatever the ISO already holds.
#
# The host and the target disks come from flags or from pickers.
#
# A host that fetches secrets also gets its identity here, before the first
# boot: the age key and deploy key are generated (or carried over) into the
# state directory on the new disk, and the enrollment file is shown for the
# maintainer to register.
{ pkgs, self }:
let
  secretsSync = import ../secrets-sync.nix { inherit pkgs; };
in
pkgs.writeShellApplication {
  name = "os-install";
  runtimeInputs = (with pkgs; [
    jq fzf util-linux coreutils nixos-install-tools age sops git openssh qrencode
  ]) ++ [ secretsSync.sync secretsSync.identity ];
  text = ''
    usage() {
      cat >&2 <<'EOF'
    usage: os-install [-H HOST] [-d [NAME=]DEVICE]... [--flake REF] [--yes]
                      [--identity FILE] [--no-enroll] [--enroll-out PATH]

      -H, --host HOST        host to install (a directory under hosts/)
      -d, --disk [NAME=]DEV  physical device for a disk in the host's layout;
                             NAME may be omitted when the layout has one disk
          --flake REF        flake to install from (default: the one running this);
                             a private one, git+ssh://…, the host reads with its
                             own configuration key, so its identity is made
                             first, in memory, and the install waits until its
                             keys are registered
      -y, --yes              do not ask for confirmation before erasing
          --identity FILE    carry over the identity document written by
                             'fleet-identity --export' on the old install,
                             instead of generating a new identity
          --no-enroll        do not wait for the host to be enrolled
          --enroll-out PATH  also copy the enrollment file to PATH, for
                             example on a mounted USB stick

    Anything not given as a flag is chosen from a list, when run in a terminal.
    A host with an encrypted layout asks for one passphrase, used for every
    encrypted disk.
    Whether a firmware boot entry is written follows the host's
    conf.hardware.boot.efiVariables.
    EOF
      exit "''${1:-1}"
    }

    # Partitioning and installing both need root. On the NixOS ISO the default
    # user has passwordless sudo.
    if [ "$(id -u)" -ne 0 ]; then
      exec sudo "$0" "$@"
    fi

    passfile=""
    ramState=""
    cleanup() {
      if [ -n "$passfile" ]; then
        rm -f "$passfile"
      fi
      # The identity made in memory for a private flake; by now it is on the
      # new disk, or the install did not happen.
      if [ -n "$ramState" ]; then
        rm -rf "$ramState"
      fi
    }
    trap cleanup EXIT

    # The installer ISO does not enable flakes, and sudo above drops the
    # caller's environment, so turn them on here for every nix call below.
    export NIX_CONFIG="experimental-features = nix-command flakes"

    # path: so Nix reads the store copy as a plain directory; a bare store path
    # is taken for a git repository, which root-owned store paths fail as. A
    # path: flake has no revision of its own, so it is passed along: the
    # installed system records it, and the first-boot clone checks it out.
    ownFlake="path:${self}${pkgs.lib.optionalString (self ? rev) "?rev=${self.rev}"}"
    flake="$ownFlake"
    host=""
    assumeYes=false
    confirm=""
    declare -A disks=()
    unnamedDisk=""
    identity=""
    noEnroll=false
    enrollOut=""

    while [ $# -gt 0 ]; do
      case "$1" in
        -H|--host)  host="''${2:?--host needs a value}"; shift 2 ;;
        -d|--disk)
          case "''${2:?--disk needs a value}" in
            *=*) disks["''${2%%=*}"]="''${2#*=}" ;;
            *)   unnamedDisk="$2" ;;
          esac
          shift 2 ;;
        --flake)    flake="''${2:?--flake needs a value}"; shift 2 ;;
        -y|--yes)   assumeYes=true; shift ;;
        --identity) identity="''${2:?--identity needs a value}"; shift 2 ;;
        --no-enroll) noEnroll=true; shift ;;
        --enroll-out) enrollOut="''${2:?--enroll-out needs a value}"; shift 2 ;;
        -h|--help)  usage 0 ;;
        *)          echo "os-install: unknown argument '$1'" >&2; usage ;;
      esac
    done

    ${pkgs.lib.optionalString (!(self ? rev)) ''
    if [ "$flake" = "$ownFlake" ]; then
      echo "os-install: this flake has uncommitted changes, so the installed system" >&2
      echo "  records no revision, and its configuration checkout starts at the tip" >&2
      echo "  of the tracked branch, which may not match what is installed." >&2
    fi
    ''}
    if [ -n "$identity" ]; then
      # Made absolute because the init step below runs from another directory.
      identity="$(realpath -e -- "$identity")" \
        || { echo "os-install: no such identity document: $identity" >&2; exit 1; }
    fi

    interactive=false
    [ -t 0 ] && [ -t 1 ] && interactive=true

    need() {  # need WHAT — fail when a value is missing and there is no terminal
      if ! $interactive; then
        echo "os-install: $1 not given, and there is no terminal to choose in" >&2
        usage
      fi
    }

    pick() {  # pick HEADER — a dropdown over stdin, printing the chosen line
      fzf --height=40% --layout=reverse --border --no-multi --header="$1" \
        || { echo "os-install: nothing chosen; nothing was changed" >&2; exit 1; }
    }

    # A private flake, git+ssh://, can only be read with the
    # host's own configuration key, so the identity comes first, made in
    # memory (/run is a tmpfs), and is enrolled before anything is fetched.
    # The flake is cloned with that key and installed from the clone, which
    # keeps its revision. The identity reaches the new disk below, as one
    # carried over with --identity does.
    private=false
    case "$flake" in git+ssh://*) private=true ;; esac
    if $private; then
      if [ -z "$host" ]; then
        echo "os-install: a private flake needs the host (-H): it cannot be read to list them yet" >&2
        exit 1
      fi
      ramState="$(umask 077; mktemp -d /run/os-install-identity.XXXXXX)"
      initArgs=(--state "$ramState" --host "$host")
      if [ -n "$identity" ]; then
        initArgs+=(--from-identity "$identity")
      fi
      fleet-secrets-sync init "''${initArgs[@]}"
      if [ -z "$identity" ]; then
        echo
        echo "Enrollment file for $host:"
        cat "$ramState/$host.enroll"
        echo
        fleet-identity --state "$ramState" --qr
        if [ -n "$enrollOut" ]; then
          cp -- "$ramState/$host.enroll" "$enrollOut"
          echo "os-install: enrollment file copied to $enrollOut"
        fi
        echo "On the maintainer's machine: fleet secrets host-enroll $host --from <that file>,"
        echo "push, then register its deploy key read-only on the secrets repository and"
        echo "its configuration key read-only on the configuration repository."
      fi
      # The document the step after nixos-install reads, as with --identity.
      fleet-identity --state "$ramState" --export > "$ramState/identity.yaml"
      identity="$ramState/identity.yaml"

      # git+ssh://git@github.com/o/r?ref=x is what git clones, and a branch.
      url="''${flake#git+}"
      cloneArgs=(-q)
      case "$url" in
        *"?ref="*) ref="''${url##*\?ref=}"; cloneArgs+=(--branch "''${ref%%&*}") ;;
      esac
      url="''${url%%\?*}"
      clone="$ramState/flake"
      export GIT_SSH_COMMAND="ssh -i $ramState/config_deploy_ed25519 -o IdentitiesOnly=yes -o UserKnownHostsFile=$ramState/known_hosts -o StrictHostKeyChecking=accept-new"
      attempt=0
      until git clone "''${cloneArgs[@]}" -- "$url" "$clone"; do
        rm -rf "$clone"
        attempt=$((attempt + 1))
        if $interactive; then
          read -r -p "Could not fetch $url yet. Once its keys are registered, press Enter to try again (Ctrl-D stops) " _ \
            || { echo; echo "os-install: stopped; nothing was changed" >&2; exit 1; }
        elif [ "$attempt" -ge 60 ]; then
          echo "os-install: could not fetch $url for 30 minutes; nothing was changed" >&2
          exit 1
        else
          echo "os-install: could not fetch $url; trying again in 30 seconds" >&2
          sleep 30
        fi
      done
      unset GIT_SSH_COMMAND
      flake="git+file://$clone"
      echo "os-install: fetched $url at $(git -C "$clone" rev-parse --short HEAD)"
    fi

    if [ -z "$host" ]; then
      need "the host"
      host="$(nix eval --json "$flake#nixosConfigurations" --apply builtins.attrNames \
        | jq -r '.[]' | pick "Host to install from $flake")"
    fi

    # Everything about the host this needs, in one evaluation.
    facts="$(nix eval --json "$flake#nixosConfigurations.$host.config" --apply 'c: {
      disks = builtins.mapAttrs (_: d: d.device) c.disko.devices.disk;
      swap = map (s: s.device) c.swapDevices;
      bios = c.conf.hardware.boot.mode == "bios";
      luks = builtins.attrValues (builtins.mapAttrs (_: d: d.device) c.boot.initrd.luks.devices);
      tpm2 = c.conf.hardware.boot.tpm2Unlock;
      secrets = c.conf.secrets.repo.url != null;
      secretsRepo = c.conf.secrets.repo.url;
      secretsRef = c.conf.secrets.repo.ref;
      statePath = c.conf.secrets.statePath;
      signers = let f = c.conf.fleet.signing.allowedSignersFile; in
        if f == null then null else toString f;
      repo = c.conf.fleet.repo.url;
    }')"

    mapfile -t declared < <(jq -r '.disks | keys[]' <<<"$facts")
    if [ "''${#declared[@]}" -eq 0 ]; then
      echo "os-install: host '$host' declares no disk layout: set conf.hardware.disk.enable or add hosts/$host/disko-config.nix" >&2
      exit 1
    fi

    if [ -n "$unnamedDisk" ]; then
      if [ "''${#declared[@]}" -ne 1 ]; then
        echo "os-install: '$host' declares disks ''${declared[*]}; name the one '$unnamedDisk' is for: -d NAME=$unnamedDisk" >&2
        exit 1
      fi
      disks["''${declared[0]}"]="$unnamedDisk"
    fi

    # The disk this installer booted from is never offered.
    bootMedium=""
    if src="$(findmnt -no SOURCE /iso 2>/dev/null)" && [ -n "$src" ]; then
      bootMedium="/dev/$(lsblk -no PKNAME "$src" | head -1)"
    fi

    for name in "''${declared[@]}"; do
      if [ -z "''${disks[$name]:-}" ]; then
        need "a device for disk '$name'"
        taken=" ''${disks[*]} $bootMedium "
        disks[$name]="$(lsblk --nodeps --noheadings --exclude 7,11 --output PATH,SIZE,TRAN,MODEL \
          | while read -r path rest; do
              case "$taken" in *" $path "*) ;; *) printf '%-14s %s\n' "$path" "$rest" ;; esac
            done \
          | pick "Device for disk '$name' of $host (it will be erased)" | cut -d' ' -f1)"
      fi
      if [ ! -b "''${disks[$name]}" ]; then
        echo "os-install: ''${disks[$name]} is not a block device" >&2
        exit 1
      fi
      if [ "''${disks[$name]}" = "$bootMedium" ]; then
        echo "os-install: ''${disks[$name]} is the installer's own boot medium" >&2
        exit 1
      fi
      # GRUB on BIOS installs to the device written in the layout, not to the
      # one chosen here, so the two must agree.
      if [ "$(jq -r '.bios' <<<"$facts")" = true ] \
         && [ "$(jq -r --arg n "$name" '.disks[$n]' <<<"$facts")" != "''${disks[$name]}" ]; then
        echo "os-install: '$host' boots with BIOS; set disko.devices.disk.$name.device to ''${disks[$name]} in its layout first" >&2
        exit 1
      fi
    done

    echo
    echo "Installing $host from $flake"
    for name in "''${!disks[@]}"; do
      echo "  $name -> ''${disks[$name]}  (ALL DATA ON IT WILL BE ERASED)"
    done
    if ! $assumeYes; then
      need "confirmation (or --yes)"
      read -r -p "Type the host name to continue: " confirm
      if [ "$confirm" != "$host" ]; then
        echo "os-install: not confirmed; nothing was changed" >&2
        exit 1
      fi
    fi

    # One passphrase for every LUKS container, kept in a file only root can
    # read under /run (a tmpfs) for disko to read; the exit trap removes it.
    if [ "$(jq -r '.luks | length' <<<"$facts")" -gt 0 ]; then
      need "the disk passphrase"
      while true; do
        read -rs -p "Passphrase for the encrypted disks: " pass1; echo
        read -rs -p "Repeat the passphrase: " pass2; echo
        if [ -z "$pass1" ]; then
          echo "os-install: the passphrase may not be empty" >&2
        elif [ "$pass1" != "$pass2" ]; then
          echo "os-install: the passphrases differ" >&2
        else
          break
        fi
      done
      passfile="$(umask 077; mktemp /run/os-install-passphrase.XXXXXX)"
      printf '%s' "$pass1" > "$passfile"
      unset pass1 pass2
    fi

    # Step 1: the host's disko script with the chosen devices patched in.
    mapping="{}"
    for name in "''${!disks[@]}"; do
      mapping="$(jq -c --arg n "$name" --arg d "''${disks[$name]}" '. + {($n): $d}' <<<"$mapping")"
    done
    export OS_INSTALL_DISKS="$mapping" OS_INSTALL_FLAKE="$flake" OS_INSTALL_HOST="$host"
    export OS_INSTALL_PASSFILE="$passfile"
    # shellcheck disable=SC2016 # a Nix expression; its interpolations are for Nix, not bash
    diskoScript="$(nix build --impure --no-link --print-out-paths --expr '
      let
        flake = builtins.getFlake (builtins.getEnv "OS_INSTALL_FLAKE");
        host = flake.nixosConfigurations.''${builtins.getEnv "OS_INSTALL_HOST"};
        lib = flake.inputs.nixpkgs.lib;
        devices = builtins.fromJSON (builtins.getEnv "OS_INSTALL_DISKS");
        passfile = builtins.getEnv "OS_INSTALL_PASSFILE";
        # Every LUKS container reads the one passphrase from the same file;
        # other partitions are left as the layout declares them.
        luksOnly = lib.filterAttrs (_: part: (part.content.type or "") == "luks");
        patched = host.extendModules { modules = [
          { disko.devices.disk = lib.mapAttrs (_: device: { device = lib.mkForce device; }) devices; }
          (lib.mkIf (passfile != "") {
            disko.devices.disk = lib.mapAttrs
              (_: d: {
                content.partitions = lib.mapAttrs
                  (_: _: { content.passwordFile = lib.mkForce passfile; })
                  (luksOnly (d.content.partitions or { }));
              })
              host.config.disko.devices.disk;
          })
        ]; };
      in patched.config.system.build.diskoScript')"

    # Wipes, formats and mounts at /mnt. An encrypted layout takes the
    # passphrase given above.
    "$diskoScript"

    # disko formats swap but its mount step leaves it off (its fs entries are
    # ordered by mount point, and swap has none), so turn on what the host
    # declares. The evaluation and build in step 2 can then page to the new
    # disk; on an encrypted layout the swap is inside LUKS.
    while read -r swap; do
      if [ -b "$swap" ] && ! swapon --show=NAME --noheadings | grep -qx "$(readlink -f "$swap")"; then
        if swapon "$swap"; then
          echo "os-install: swap on $swap"
        else
          echo "os-install: warning: could not turn on swap $swap; continuing in RAM only" >&2
        fi
      fi
    done < <(jq -r '.swap[]' <<<"$facts")

    # Step 2: build and download straight onto the new disk.
    nixos-install --flake "$flake#$host" --root /mnt --no-root-passwd --no-channel-copy

    # The identity goes onto the new disk while /mnt is still mounted.
    enrollSkipped=false
    if [ "$(jq -r '.secrets' <<<"$facts")" = true ]; then
      statePath="$(jq -r '.statePath' <<<"$facts")"
      state="/mnt$statePath"
      initArgs=(--state "$state" --host "$host")
      if [ -n "$identity" ]; then
        initArgs+=(--from-identity "$identity")
      fi
      fleet-secrets-sync init "''${initArgs[@]}"

      if [ -z "$identity" ]; then
        echo
        echo "Enrollment file for $host:"
        cat "$state/$host.enroll"
        echo
        fleet-identity --state "$state" --qr
        if [ -n "$enrollOut" ]; then
          cp -- "$state/$host.enroll" "$enrollOut"
          echo "os-install: enrollment file copied to $enrollOut"
        fi
        if $noEnroll; then
          enrollSkipped=true
        elif $interactive; then
          echo "On the maintainer's machine: fleet-secrets host-enroll $host --from <that file>"
          if ! read -r -p "Enroll the host now on the maintainer's machine, then press Enter (Ctrl-D skips) " _; then
            echo
            enrollSkipped=true
          fi
        fi
      fi

      secretsRepo="$(jq -r '.secretsRepo' <<<"$facts")"
      secretsRef="$(jq -r '.secretsRef' <<<"$facts")"
      signers="$(jq -r '.signers // empty' <<<"$facts")"
      if $enrollSkipped; then
        echo "os-install: enrollment skipped; the secrets are fetched after boot"
      elif [ -z "$signers" ]; then
        echo "os-install: warning: $host sets no conf.fleet.signing.allowedSignersFile; secrets not fetched" >&2
      else
        rc=0
        fleet-secrets-sync sync --state "$state" --host "$host" \
          --repo "$secretsRepo" --ref "$secretsRef" --signers "$signers" || rc=$?
        case "$rc" in
          0)  echo "os-install: secrets in place" ;;
          15) echo "os-install: secrets fetched but not yet decryptable by this host: finish enrollment, the host retries at boot" >&2 ;;
          10) echo "os-install: deploy key not accepted yet: register it, the host retries at boot" >&2 ;;
          *)  echo "os-install: secrets sync exited with code $rc (see the message above); the host retries at boot" >&2 ;;
        esac
      fi
    fi

    swapoff --all || true

    echo
    echo "Installed $host. Remove the installer media and reboot. Then:"
    if [ "$(jq -r '.repo != null' <<<"$facts")" = true ]; then
      echo "  - On first boot with network, the machine clones its configuration into"
      echo "    /etc/nixos and starts syncing from it."
    fi
    if [ "$(jq -r '.tpm2' <<<"$facts")" = true ]; then
      echo "  - To unlock with the TPM as well as the passphrase (needs a TPM2 chip):"
      jq -r '.luks[] | "      sudo systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7 " + .' <<<"$facts"
    fi
    if $enrollSkipped; then
      echo "  - Enrollment was skipped. The enrollment file is $statePath/$host.enroll on the"
      echo "    installed system; 'sudo fleet-identity --qr' shows it after boot."
    fi
  '';
}
