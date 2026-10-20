{ lib, ... }:
{
  config = {
    services.journald.extraConfig = lib.mkDefault ''
      SystemMaxUse=1G
      MaxRetentionSec=90day
    '';
    # Sending it elsewhere: loki.nix, with conf.fleet.logs.lokiUrl.
  };
}
