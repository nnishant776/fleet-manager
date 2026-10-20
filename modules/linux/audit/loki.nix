# The journal, sent to the fleet's Loki (conf.fleet.logs.lokiUrl), so that
# what happened on a machine can be read when the machine cannot, or no
# longer can be trusted to say. The journal holds the audit records too
# (auditd.nix): journald reads them alongside auditd. Grafana Alloy does the
# sending; Promtail, which did it before, is no longer developed.
{ config, lib, ... }:
let
  url = config.conf.fleet.logs.lokiUrl;
in
{
  config = lib.mkIf (url != null) {
    services.alloy.enable = true;

    # A few labels to find things by; the rest stays in the log line, as
    # Loki expects. Alloy keeps its place in the journal under
    # /var/lib/alloy, so a restart sends nothing twice and skips nothing.
    environment.etc."alloy/fleet.alloy".text = ''
      loki.relabel "journal" {
        forward_to = []
        rule {
          source_labels = ["__journal__systemd_unit"]
          target_label  = "unit"
        }
        rule {
          source_labels = ["__journal_priority_keyword"]
          target_label  = "level"
        }
        rule {
          source_labels = ["__journal__transport"]
          target_label  = "transport"
        }
      }

      loki.source.journal "journal" {
        forward_to    = [loki.write.fleet.receiver]
        relabel_rules = loki.relabel.journal.rules
        labels        = { host = ${builtins.toJSON config.conf.host.name} }
      }

      loki.write "fleet" {
        endpoint {
          url = ${builtins.toJSON url}
        }
      }
    '';
  };
}
