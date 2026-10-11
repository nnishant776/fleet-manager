{ lib, ... }: {
  options.conf.secrets = {
    repo = {
      url = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "git@github.com:org/fleet-secrets.git";
        description = ''
          The private secrets repository this host fetches its secrets from.
          `null` opts the host out entirely: no secrets sync, no host
          identity, sops off, and `conf.users.accounts.<name>.secrets.*` are
          ignored.

          On NixOS it must be an SSH URL, since the host authenticates with
          its deploy key, and it requires `conf.fleet.signing.allowedSignersFile`
          and `conf.fleet.repo.url`: the fleet sync refreshes the secrets.
        '';
      };
      ref = lib.mkOption {
        type = lib.types.str;
        default = "main";
        description = "Branch of the secrets repository to track.";
      };
    };

    statePath = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/fleet";
      description = ''
        Where the host's identity, enrollment file and secrets snapshot live,
        including the deploy key at `deploy_ed25519`. Expected to be its own
        volume. Unused when `repo.url` is `null`.
      '';
    };
  };
}
