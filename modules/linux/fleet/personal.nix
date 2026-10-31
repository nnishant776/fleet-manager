# A host handed out to one person (conf.fleet.personal). The owner makes their
# own account at the first boot, on the console, with no help from the
# maintainer. The maintainer keeps the system updated (the sync, as on any
# fleet host) and the support account. The owner's account is local to the
# machine: it is not in the configuration, so accounts here are mutable, and
# it is not in the inventory until the maintainer adopts it.
{ config, lib, pkgs, utils, ... }:
let
  cfg = config.conf.fleet.personal;
  support = config.conf.fleet.support;

  marker = "/var/lib/fleet/owner";

  # The support account exists when conf.fleet.support made it.
  hasSupport = config.conf.users.accounts ? ${support.user};

  # Names the owner may not take: every account the configuration declares
  # (root and the support account among them). The script also refuses any
  # name the machine already has.
  reserved = lib.unique ([ "root" support.user ] ++ lib.attrNames config.users.users);

  # Groups for the owner. Each is added only if the machine has it, so a host
  # without sshd or with fewer services is not an error. NetworkManager's
  # blanket polkit rules follow the networkmanager group; an unprivileged owner
  # gets the allowlist of network-users instead (host/privilege.nix).
  adminGroups = [ "wheel" "networkmanager" "ssh-users" "fleet-local" "docker" "podman" "libvirtd" ];
  plainGroups = [ "network-users" "ssh-users" "fleet-local" ];
  ownerGroups = if cfg.ownerPrivileged then adminGroups else plainGroups;

  setup = pkgs.writeShellApplication {
    name = "fleet-owner-setup";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.gnugrep
      pkgs.shadow
      pkgs.ncurses
      pkgs.networkmanager
      pkgs.getent
    ];
    text = ''
      reserved=(${lib.escapeShellArgs reserved})
      wanted=(${lib.escapeShellArgs ownerGroups})
      name=""
      full=""
      pw=""
      lines=0
      idle=0

      # Reads a line into the variable named by $1. A false return is
      # Ctrl-D at the prompt.
      ask() {
        local line
        IFS= read -r -p "$2" line || return 1
        lines=$((lines + 1))
        printf -v "$1" '%s' "$line"
      }

      # Prints why a name is refused, and returns false, or returns true.
      name_ok() {
        local n=$1 r re='^[a-z_][a-z0-9_-]{0,30}$'
        if ! [[ $n =~ $re ]]; then
          echo "A username starts with a lowercase letter or _ and has only lowercase letters, digits, _ and -, at most 31 characters."
          return 1
        fi
        for r in "''${reserved[@]}"; do
          if [ "$n" = "$r" ]; then
            echo "$n is reserved for this machine. Choose another."
            return 1
          fi
        done
        if getent passwd "$n" >/dev/null || getent group "$n" >/dev/null; then
          echo "$n is taken. Choose another."
          return 1
        fi
        return 0
      }

      # Asks for the name, full name and password. A false return is Ctrl-D,
      # and the caller starts over.
      collect() {
        local again=""
        while :; do
          ask name "Username: " || return 1
          if name_ok "$name"; then break; fi
        done
        while :; do
          ask full "Full name (may be empty): " || return 1
          if [[ $full =~ ^[^:[:cntrl:]]*$ ]]; then break; fi
          echo "The name cannot hold a colon or control characters."
        done
        while :; do
          IFS= read -rs -p "Password: " pw || { echo; return 1; }
          lines=$((lines + 1))
          echo
          if [ -z "$pw" ]; then
            echo "The password cannot be empty."
            continue
          fi
          IFS= read -rs -p "Password again: " again || { echo; return 1; }
          echo
          if [ "$pw" = "$again" ]; then break; fi
          echo "The two passwords differ. Try again."
        done
      }

      # Makes the account. False if something failed; then nothing is left.
      create() {
        local g groups=()
        for g in "''${wanted[@]}"; do
          if getent group "$g" >/dev/null; then groups+=("$g"); fi
        done
        if ! useradd -m -c "$full" -s ${utils.toShellPath config.users.defaultUserShell} \
            -G "$(IFS=,; echo "''${groups[*]}")" "$name"; then
          return 1
        fi
        # printf is a builtin: the password stays off every command line.
        if ! printf '%s:%s\n' "$name" "$pw" | chpasswd; then
          userdel -r "$name" || true
          return 1
        fi
        return 0
      }

      # Kernel messages (audit records among them) are written over the
      # screen and the prompts; keep only the serious ones while the setup
      # runs, and put the level back when it ends.
      if read -r level _ < /proc/sys/kernel/printk 2>/dev/null; then
        trap 'echo "$level" > /proc/sys/kernel/printk 2>/dev/null || true' EXIT
        echo 1 > /proc/sys/kernel/printk 2>/dev/null || true
      fi

      clear || true
      cat <<'EOF'
      This machine is set up for one person: you.
      You create your account now.

      The maintainer keeps the system updated${lib.optionalString hasSupport " and can log in as ${support.user} to help"}.
      ${if cfg.ownerPrivileged then ''
      Your account is an administrator's: it can see that${lib.optionalString hasSupport " access"} and could change it.
      '' else ''
      Your account is an ordinary one: it can join and switch networks, but cannot change the system.
      ''}
      EOF
      echo

      if ! nm-online -q -t 10; then
        echo "This machine is not on a network."
        answer=""
        read -r -p "Join a Wi-Fi network now? [y/N] " answer || answer=""
        case "$answer" in
          y|Y|yes|YES) nmtui-connect || true ;;
          *) ;;
        esac
        echo
      fi

      while :; do
        before=$lines
        if ! collect; then
          echo
          echo "Starting over."
          # Ctrl-D is a person starting over. The same without a single line
          # read, again and again, is a console that has gone away; leave, and
          # systemd starts the setup again on a fresh one.
          if [ "$lines" = "$before" ]; then idle=$((idle + 1)); else idle=0; fi
          if [ "$idle" -ge 20 ]; then exit 1; fi
          sleep 1
          continue
        fi
        if create; then break; fi
        echo "The account could not be made. Starting over."
      done

      # mkdir, not install -d: on a host with secrets the directory is the
      # host's identity, mode 0700, and its mode stays as it is.
      mkdir -p /var/lib/fleet
      (umask 022; printf '%s\n' "$name" > ${marker})
      echo
      echo "Your account $name is ready. Log in below."
      sleep 2
    '';
  };

  # Members of fleet-local also take keys from their own home; every other
  # account keeps what the rest of the configuration lists.
  sshdFiles = lib.unique ([ "%h/.ssh/authorized_keys" ] ++ config.services.openssh.authorizedKeysFiles);
in
{
  config = lib.mkIf (cfg.enable && config.conf.platform == "nixos") {
    assertions = [
      {
        assertion = config.conf.networking.backend == "networkmanager";
        message = "personal mode lets the owner manage Wi-Fi with NetworkManager;"
          + " conf.networking.backend must be \"networkmanager\" on '${config.conf.host.name}'.";
      }
    ];

    # The owner's account is made on the machine and stays across syncs.
    users.mutableUsers = true;

    # An account here may read keys from its own ~/.ssh/authorized_keys.
    # Not a privilege: sshd still needs ssh-users, and the accounts the
    # configuration declares are not in this group.
    users.groups.fleet-local = { };
    services.openssh.extraConfig = lib.mkIf config.services.openssh.enable ''
      Match Group fleet-local
        AuthorizedKeysFile ${toString sshdFiles}
    '';

    # The first boot: the console asks for the owner's account before it asks
    # anyone to log in. Not restarted by an activation, so a sync cannot reset a
    # setup in progress; and never run once the marker exists.
    #
    # The login prompt on tty1 comes after the setup (Before=): a oneshot is
    # finished only when the script is, and the prompt's start waits for that,
    # so the two never hold the console at once. Nothing stops a prompt that
    # already runs, which is why there is no Conflicts= here: with it, anyone
    # starting this unit by hand would stop the prompt and the session in it,
    # even when the marker makes the unit skip. The cost is that multi-user.target
    # (and the graphical session that follows it) waits for the owner to finish,
    # which is wanted before an account exists; ssh and the support account do
    # not wait. The other consoles (tty2 and on) are not held.
    systemd.services.fleet-owner-setup = {
      description = "Create the account of this machine's owner";
      wantedBy = [ "multi-user.target" ];
      after = [ "NetworkManager.service" "systemd-user-sessions.service" ];
      before = [ "getty@tty1.service" ];
      restartIfChanged = false;
      stopIfChanged = false;
      unitConfig.ConditionPathExists = "!${marker}";
      serviceConfig = {
        Type = "oneshot";
        TimeoutStartSec = "infinity";
        ExecStart = lib.getExe setup;
        StandardInput = "tty";
        StandardOutput = "tty";
        TTYPath = "/dev/tty1";
        TTYReset = true;
        TTYVHangup = true;
        TTYVTDisallocate = true;
        # A script that ends without an account asks again.
        Restart = "on-failure";
        RestartSec = 2;
      };
    };
  };
}
