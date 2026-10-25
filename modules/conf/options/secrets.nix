{ config, lib, ... }: {
  options.conf.secrets = {
    repo = {
      url = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "git@github.com:example-org/secrets.git";
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
        default = "host/${config.conf.host.name}";
        defaultText = lib.literalExpression ''"host/''${config.conf.host.name}"'';
        description = ''
          Branch of the secrets repository to track. Each host has a branch
          of its own, `host/<name>`, holding its files and no other host's;
          `fleet secrets host-enroll` creates it.
        '';
      };
    };

    statePath = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/fleet";
      description = ''
        Where the host's identity, enrollment file and secrets snapshot live:
        its age key, `age.key`, and its two deploy keys,
        `secrets_deploy_key` for the secrets repository and
        `config_deploy_key` for a private configuration. Expected to be its
        own volume. Unused when `repo.url` is `null`.
      '';
    };
  };
}
