# sops-nix wiring. The host's age identity and its snapshot of the secrets
# repository live under conf.secrets.statePath; fleet-identity and the sync
# service own them, so sops-nix must neither generate a key nor look for the
# encrypted files while the configuration is evaluated.
{ config, lib, ... }:
let
  cfg = config.conf.secrets;
  host = config.conf.host.name;
in {
  config = lib.mkIf (cfg.repo.url != null) {
    # A string, not a path: the file exists only on the host, so nothing enters
    # the store.
    sops.defaultSopsFile = "${cfg.statePath}/secrets/hosts/${host}.yaml";
    # The file is not available at evaluation time.
    sops.validateSopsFiles = false;
    sops.age.keyFile = "${cfg.statePath}/age.key";
    # os-install and fleet-bootstrap own the identity.
    sops.age.generateKey = false;
  };
}
