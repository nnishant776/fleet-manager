{ lib, ... }: {
  imports = [
    (lib.mkRemovedOptionModule [ "conf" "fleet" "repo" "deployKeySecret" ] ''
      A host now fetches an SSH conf.fleet.repo.url with its own configuration
      key, which it makes with its identity; no key goes into its secrets.
    '')
  ];

  options.conf.fleet = {
    repo = {
      url = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        example = "https://github.com/org/fleet-manager.git";
        description = ''
          Git remote this machine's configuration is deployed from.
          `os-install` clones it to `conf.fleet.localPath` and `autoUpdate`
          syncs from it. `null` means the machine has no upstream, in which case
          the checkout is left empty and automatic updates stay off.

          An `https://` address is fetched with no key, for a public
          repository. An SSH address (`git@host:org/repo.git` or `ssh://…`),
          for a private one, is fetched with the host's own configuration key,
          `<conf.secrets.statePath>/config_deploy_ed25519`, which the host makes with
          the rest of its identity, so it needs `conf.secrets.repo.url`. Its
          enrollment file lists the public half; register that read-only on
          the repository.
        '';
      };
      ref = lib.mkOption {
        type = lib.types.str;
        default = "main";
        description = ''
          Branch or tag to track. Point a canary group of machines at one ref
          and the rest at another to stage a rollout.
        '';
      };
    };

    localPath = lib.mkOption {
      type = lib.types.str;
      default = "/etc/nixos";
      description = ''
        Where the configuration checkout lives on the machine. The default is
        where `nixos-rebuild` looks when given no flake, so leaving it alone
        means a bare `nixos-rebuild switch` works.

        It is a git checkout rather than a copy, so `git -C <path> rev-parse
        HEAD` states which revision the machine should be running. On NixOS
        it is trusted in /etc/gitconfig (`safe.directory`), so every account
        can use it although root owns it.
      '';
    };

    signing.allowedSignersFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = lib.literalExpression "../../fleet/allowed_signers";
      description = ''
        SSH allowed-signers file (`<principal> <key-type> <key>` per line) that
        every commit fetched on `repo.ref` must be signed by. Required when
        `repo.url` is set: the sync verifies `FETCH_HEAD` against it before
        anything is built, so a compromised remote cannot push configuration
        to the fleet. Public keys only; the file is copied into the store.
      '';
    };

    support = {
      enable = lib.mkOption {
        type = lib.types.nullOr lib.types.bool;
        default = null;
        description = ''
          Create an account for the maintainer to reach this host with, so no
          per-host secret is needed for support.

          `null` means on when `conf.secrets.repo.url` is set and off when it
          is not. Set `true` to create the account on a host that has not opted
          in to secrets. NixOS only: no Darwin host gets the account yet.
        '';
      };
      user = lib.mkOption {
        type = lib.types.str;
        default = "support";
        description = "Name of the support account.";
      };
      sshKeys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = ''
          The maintainer's public SSH keys, ideally a hardware-backed
          `sk-ssh-ed25519` key. Required when the account is enabled. The
          account is privileged and has a sudo rule without a password, since
          its password is locked and the key is the authentication factor.
        '';
      };
      consolePassword = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Also take a password hash for the account from `hosts/<host>.yaml`
          (`support_password`), for console login when there is no network.
        '';
      };
    };

    autoUpdate = {
      enable = lib.mkOption {
        type = lib.types.nullOr lib.types.bool;
        default = null;
        description = ''
          Periodically sync `conf.fleet.repo` into `localPath` and rebuild from
          it.

          `null` means on when `conf.fleet.repo.url` is set and off when it is
          not, so a machine syncs by virtue of having been given a repository.
          Set `false` to opt a machine out while still pointing it at one.
          Setting `true` without a `repo.url` is an error.
        '';
      };
      rollbackOnFailure = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          If activating the new configuration fails, return the system to the
          generation it was running. On by default, so that a machine whose new
          generation breaks networking can still be reached and retried.

          A rolled-back machine no longer matches its checked-out revision until
          upstream is fixed, which is a condition worth reporting. Turn this off
          to leave a failed activation in place instead.
        '';
      };
      dates = lib.mkOption {
        type = lib.types.str;
        default = "04:00";
        description = "systemd `OnCalendar` expression for the sync timer.";
      };
      randomizedDelaySec = lib.mkOption {
        type = lib.types.int;
        default = 1800;
        description = "Jitter added to each run so that a fleet does not hit the binary cache simultaneously.";
      };
      allowReboot = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Reboot after a switch when the new kernel or initrd differs from the
          running one. Off by default, since the new userspace is active either
          way and an unattended reboot is rarely wanted on a workstation.
        '';
      };
      resetLocalChanges = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Hard-reset `localPath` to the tracked ref before building, discarding
          any local edits. On by default, so that the checked-out revision
          always describes the machine. With it off, a machine that has
          diverged stops converging instead.
        '';
      };
    };
  };
}
