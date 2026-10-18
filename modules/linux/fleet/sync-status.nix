# Tells everyone at a fleet machine when its configuration has stopped
# updating, so that the problem gets reported quickly. The sync
# (fleet/sync.nix) writes /var/lib/fleet-sync/status when a run stops on
# something a person has to fix, such as an unsigned commit or a build that
# fails, and removes it when a run succeeds. Every account sees it: as a
# persistent notification in a graphical session, shown again whenever the
# sync writes it, and as a few lines at every shell login, which also covers
# SSH and headless machines. `fleet-sync-status` prints it on demand.
{ config, lib, pkgs, ... }:
let
  cfg = config.conf.fleet;
  au = cfg.autoUpdate;
  # As in fleet/sync.nix: the notice exists wherever the sync does.
  enabled = if au.enable != null then au.enable else cfg.repo.url != null;

  status = "/var/lib/fleet-sync/status";

  # The status, for people: what happened, what to do, and since when.
  describe = pkgs.writeShellApplication {
    name = "fleet-sync-status";
    runtimeInputs = [ pkgs.coreutils pkgs.gnused ];
    text = ''
      [ -r ${status} ] || { echo "Configuration updates are working."; exit 0; }
      field() { sed -n "s/^$1=//p" ${status}; }
      since="$(field since)"
      commit="$(field commit)"
      field message
      echo
      echo "Please report this to your administrator."
      if [ -n "$since" ]; then
        printf 'Since %s%s.\n' "$(date -d "$since" '+%-d %b %Y, %H:%M')" "''${commit:+, commit ''${commit:0:12}}"
      fi
    '';
  };

  notify = pkgs.writeShellApplication {
    name = "fleet-sync-notify";
    runtimeInputs = [ pkgs.coreutils pkgs.libnotify pkgs.systemd describe ];
    text = ''
      idFile="''${XDG_RUNTIME_DIR:-/tmp}/fleet-sync-notify.id"

      # Nothing wrong, and nothing on screen to take down.
      if [ ! -r ${status} ] && [ ! -s "$idFile" ]; then
        exit 0
      fi

      # As in fleet/home-drift.nix: the notification daemon is whatever the
      # session runs, and claims its name a moment after the session starts.
      for _ in $(seq 30); do
        busctl --user --quiet status org.freedesktop.Notifications >/dev/null 2>&1 && break
        sleep 2
      done
      if ! busctl --user --quiet status org.freedesktop.Notifications >/dev/null 2>&1; then
        echo "no notification daemon on the session bus after 60 s"
        exit 0
      fi

      if [ ! -r ${status} ]; then
        # Fixed: take down the notice this session showed.
        busctl --user call org.freedesktop.Notifications /org/freedesktop/Notifications \
          org.freedesktop.Notifications CloseNotification u "$(cat "$idFile")" || true
        rm -f "$idFile"
        exit 0
      fi

      # Persistent by spec-level means only, as for the home-drift notice: no
      # expiry, critical urgency and the resident hint. Each showing replaces
      # the last one by id rather than stacking a second copy.
      replaceId="$(cat "$idFile" 2>/dev/null || echo 0)"
      notify-send \
        --app-name="Fleet configuration" \
        --urgency=critical \
        --expire-time=0 \
        --hint=boolean:resident:true \
        --icon=dialog-warning \
        --replace-id="$replaceId" \
        --print-id \
        "Configuration updates on this machine have stopped" \
        "$(fleet-sync-status)" > "$idFile"
    '';
  };
in {
  config = lib.mkIf (enabled && cfg.repo.url != null && config.conf.platform == "nixos") (lib.mkMerge [
    {
      environment.systemPackages = [ describe ];

      environment.loginShellInit = ''
        if [ -r ${status} ]; then
          printf '\n*** Configuration updates on this machine have stopped ***\n\n'
          ${lib.getExe describe}
          echo
        fi
      '';
    }

    (lib.mkIf config.conf.desktop.enable {
      systemd.user.services.fleet-sync-notify = {
        description = "Show whether this machine's configuration updates have stopped";
        wantedBy = [ "graphical-session.target" ];
        after = [ "graphical-session.target" ];
        partOf = [ "graphical-session.target" ];
        serviceConfig = {
          Type = "oneshot";
          ExecStart = lib.getExe notify;
        };
      };

      # The sync replaces the file on every run that stops and removes it on
      # one that succeeds; either starts the service above.
      systemd.user.paths.fleet-sync-notify = {
        description = "Watch this machine's configuration update status";
        wantedBy = [ "graphical-session.target" ];
        partOf = [ "graphical-session.target" ];
        pathConfig.PathChanged = status;
      };

      # As in fleet/home-drift.nix: wantedBy only takes effect at the next
      # login, so start both in a graphical session that was already running
      # when the switch added them.
      system.userActivationScripts.fleetSyncNotify = ''
        if ${pkgs.systemd}/bin/systemctl --user is-active --quiet graphical-session.target; then
          ${pkgs.systemd}/bin/systemctl --user start --no-block fleet-sync-notify.path fleet-sync-notify.service || true
        fi
      '';
    })
  ]);
}
