{ lib, ... }:
let
  # Keyed by username, so the attribute name is the account name. Only
  # interactive accounts belong here; service accounts are created by the
  # modules that need them.
  account = lib.types.submodule ({ name, ... }: {
    options = {
      fullName = lib.mkOption {
        type = lib.types.str;
        default = name;
        defaultText = lib.literalExpression "the attribute name";
        description = "User's full name. Defaults to the username.";
      };
      email = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "User's email address.";
      };
      privileged = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Grant this user administrative access: sudo through `wheel`, plus the
          privilege-adjacent groups for whichever services the host enables,
          namely networkmanager, wpa_supplicant, docker, podman and libvirtd.

          Those groups all confer root or near-root access, so they follow from
          this one flag rather than being listed individually in `groups`.
        '';
      };
      groups = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = ''
          Extra groups for the user, beyond those derived from `privileged` and
          from the host's enabled services. The privilege-granting groups
          (wheel, docker, podman, libvirtd, networkmanager, wpa_supplicant) are rejected here;
          set `privileged = true` instead.
        '';
      };
      initialHashedPassword = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        description = ''
          Password hash applied when the account is first created, in
          `mkpasswd -m yescrypt` format. `null` creates the account locked, so
          it is reached with an SSH key or by an administrator setting a
          password. On a host with `conf.secrets.repo.url` the hash comes
          from the secrets repository instead, and setting both is an error.
        '';
      };
      sshKeys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "SSH public keys accepted for this account's login, on a host without `conf.secrets.repo.url`. On a host with secrets they are a build error: the keys go in the account's file in the secrets repository (`secrets.authorizedKeys`).";
      };
      sudoSshKeys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = ''
          SSH public keys that unlock sudo for this privileged account instead
          of its password (NixOS). sudo checks the key held by the SSH agent
          the user brings along (`ssh -A`); without one of these keys it asks
          for the password as usual. Login keys (`sshKeys`) do not count unless
          listed here too, so the sudo key can be a separate, stronger one.
          Forward your agent only to machines you trust: while you are
          connected, root there can use your agent too. For a host without
          `conf.secrets.repo.url`; on a host with secrets they are a build
          error, and the keys go in the account's file (`secrets.sudoKeys`).
        '';
      };
      secrets = {
        password = lib.mkOption {
          type = lib.types.bool;
          default = true;
          description = "Take this account's password hash from users/<name>/<host>.yaml in the secrets repository. Ignored on a host without `conf.secrets.repo.url`.";
        };
        sshKey = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Deploy a per-host SSH key from users/<name>/<host>.yaml to /run/secrets/users/<name>/ssh_key. Can be turned on any time after the account exists. Ignored on a host without `conf.secrets.repo.url`.";
        };
        authorizedKeys = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Accept the SSH public keys in users/<name>/<host>.yaml (`authorized_keys`, one per line) for this account's login: on a host with secrets the only login keys sshd reads, since keys in the configuration are a build error there and ~/.ssh/authorized_keys is not read. They are read from /run/secrets/users/<name>/authorized_keys at each login, so a change needs no rebuild. Ignored on a host without `conf.secrets.repo.url`.";
        };
        sudoKeys = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Accept the SSH public keys in users/<name>/<host>.yaml (`sudo_keys`, one per line) for this privileged account's sudo, as `sudoSshKeys` does on a host without secrets; on a host with secrets these are the only sudo keys. Ignored on a host without `conf.secrets.repo.url`.";
        };
        own = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Decrypt this account's whole file, users/<name>/<host>.yaml, to /run/secrets/users/<name>/all, readable by the account alone (mode 0400), so that the user can read what they keep in it: `fleet-user-secret <name>` prints `secrets.<name>` from it. Meant for a user who has a branch of their own in the secrets repository and stores secrets there with `fleet user secret set`. Ignored on a host without `conf.secrets.repo.url`.";
        };
      };
      manageAccount = lib.mkOption {
        type = lib.types.nullOr lib.types.bool;
        default = null;
        description = ''
          Whether the account itself is created and maintained from this
          configuration, rather than only its home.

          `null` resolves to true on Linux and false on Darwin, where accounts
          normally come from MDM. An explicit value overrides that. `false` is
          not supported on Linux and is rejected by an assertion.

          Setting it true on Darwin adds the user to `users.knownUsers`, which is
          also nix-darwin's delete list: removing the user from
          `conf.users.accounts` later deletes the account on the next activation
          for any uid above 501. A `uid` is required in that case.
        '';
      };
      uid = lib.mkOption {
        type = lib.types.nullOr lib.types.int;
        default = null;
        description = ''
          Numeric user id, required when `manageAccount` resolves true on Darwin.

          It must match the id the account already has, or activation warns about
          an unexpected uid and leaves the user untouched. macOS assigns ids from
          501 in creation order, so one person can hold different ids on
          different machines unless MDM pins them.
        '';
      };
      gid = lib.mkOption {
        type = lib.types.nullOr lib.types.int;
        default = null;
        description = ''
          Numeric primary group id. Optional, since nix-darwin defaults to 20
          (`staff`), which is what a normal macOS account uses.
        '';
      };
      selfManagedHome = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Whether this user manages their own home instead of the system doing
          it.

          Off by default, meaning a system rebuild activates the home and the
          user's own `~/.config/home-manager/home.nix` is not read at all.
          Personal configuration for such a user belongs in `users/<name>/`.

          On, the system stops activating that home and publishes
          `homeConfigurations.<user>@<host>` for the user to activate with
          `home-switch`. The baseline, `users/<name>/` and `homeConfig` are
          still merged in either way; only the activation changes hands.
        '';
      };
      homeConfig = lib.mkOption {
        type = lib.types.deferredModule;
        default = { };
        description = "Additional Home Manager configuration for this user.";
      };
    };
  });
in {
  options.conf.users = {
    manageHomes = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Whether homes on this machine are managed at all, by either the system
        or their users. On by default; turning it off means nothing is
        delivered to any home on the host and nothing is published for users to
        activate.

        Who manages an individual home is `accounts.<name>.selfManagedHome`.
      '';
    };
    accounts = lib.mkOption {
      type = lib.types.attrsOf account;
      default = { };
      example = lib.literalExpression ''
        {
          orgadmin = { fullName = "Org Admin"; privileged = true; };
          alice = { fullName = "Alice"; selfManagedHome = true; };
        }
      '';
      description = ''
        Interactive accounts on this machine, keyed by username. Service and
        system accounts do not belong here; they are created by the modules
        that need them.

        On a host that opted into secrets (`conf.secrets.repo.url`), each
        account's `secrets.*` flags say what the host takes from its
        `users/<name>/<host>.yaml` in the secrets repository; see
        `conf.secrets`.
      '';
    };
  };
}
