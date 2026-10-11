# Same wiring as modules/linux/secrets/sops.nix. Password files do not apply:
# Darwin accounts come from MDM.
{ config, lib, ... }:
let
  cfg = config.conf.secrets;
in {
  config = lib.mkIf (cfg.repo.url != null) {
    # A string, so nothing enters the store; the snapshot only exists on the host.
    sops.defaultSopsFile = "${cfg.statePath}/secrets/hosts/${config.conf.host.name}.yaml";
    sops.validateSopsFiles = false;
    sops.age.keyFile = "${cfg.statePath}/age.key";
    sops.age.generateKey = false;
  };
}
