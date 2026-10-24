# The host side of the fleet's secrets: the sync
# script and the identity viewer. Built here, not in a module, so that the
# fleet modules and os-install install exactly the same scripts.
{ pkgs, nix ? pkgs.nix }:
let
  pins = import ./forge-host-keys.nix;
in
{
  sync = pkgs.writeShellApplication {
    name = "fleet-secrets-sync";
    runtimeInputs = with pkgs; [
      git openssh age sops yq-go coreutils util-linux findutils gnugrep nix
    ];
    text = ''
      usage() {
        cat >&2 <<'EOF'
      usage: fleet-secrets-sync init  --state DIR --host H [--from-identity FILE] [--force]
             fleet-secrets-sync sync  --state DIR --host H --repo URL [--ref main] --signers FILE
             fleet-secrets-sync check --state DIR --host H --config FLAKEREF

      exit codes: 10 fetch failed, 11 commit not signed by an allowed signer,
      12 check found missing files or keys, 13 bad state directory, 14 identity
      missing or not writable, 15 host file does not decrypt with this host's key,
      16 commit does not follow the one accepted last (a rollback)
      EOF
        exit "''${1:-1}"
      }

      fail() {  # fail CODE MESSAGE...
        local code="$1"
        shift
        echo "fleet-secrets-sync: $*" >&2
        exit "$code"
      }

      [ $# -gt 0 ] || usage
      case "$1" in
        -h|--help) usage 0 ;;
        init|sync|check) command="$1"; shift ;;
        *) echo "fleet-secrets-sync: unknown command '$1'" >&2; usage ;;
      esac

      state=/var/lib/fleet
      host=""
      from_identity=""
      force=0
      repo=""
      ref=main
      signers=""
      config=""

      while [ $# -gt 0 ]; do
        case "$1" in
          --state)         state="''${2:?--state needs a value}"; shift 2 ;;
          --host)          host="''${2:?--host needs a value}"; shift 2 ;;
          --from-identity) from_identity="''${2:?--from-identity needs a value}"; shift 2 ;;
          --force)         force=1; shift ;;
          --repo)          repo="''${2:?--repo needs a value}"; shift 2 ;;
          --ref)           ref="''${2:?--ref needs a value}"; shift 2 ;;
          --signers)       signers="''${2:?--signers needs a value}"; shift 2 ;;
          --config)        config="''${2:?--config needs a value}"; shift 2 ;;
          -h|--help)       usage 0 ;;
          *) echo "fleet-secrets-sync: unknown option '$1'" >&2; usage ;;
        esac
      done

      [ -n "$host" ] || { echo "fleet-secrets-sync: --host is required" >&2; usage; }

      # The state directory holds private keys; refuse one that somebody else
      # could have tampered with.
      check_state() {
        local owner mode
        [ -d "$state" ] || fail 13 "$state does not exist or is not a directory"
        owner="$(stat -c %U "$state")"
        mode="$(stat -c %a "$state")"
        if [ "$owner" != "$(id -un)" ] || [ $(( 8#$mode & 8#077 )) -ne 0 ]; then
          fail 13 "$state is owned by $owner with mode $mode; it must belong to $(id -un) and have no group or other access"
        fi
      }

      # The keys were once named after their type, and the secrets one earlier
      # still just deploy_ed25519; whichever runs first, init or a sync, renames
      # them once. Of two old secrets keys, the newer name is the one in use.
      rename_old_keys() {
        local pair old new
        for pair in secrets_deploy_ed25519:secrets_deploy_key \
                    deploy_ed25519:secrets_deploy_key \
                    config_deploy_ed25519:config_deploy_key; do
          old="''${pair%%:*}"
          new="''${pair##*:}"
          if [ -e "$state/$old" ] && [ ! -e "$state/$new" ]; then
            mv "$state/$old" "$state/$new"
            if [ -e "$state/$old.pub" ]; then
              mv "$state/$old.pub" "$state/$new.pub"
            fi
          fi
        done
      }

      case "$command" in
        init)
          umask 077
          mkdir -p "$state" || fail 14 "cannot create $state"
          chmod 700 "$state" || fail 14 "cannot set the mode of $state"
          cd "$state"
          rename_old_keys

          if [ -n "$from_identity" ]; then
            [ -f "$from_identity" ] || fail 14 "no such identity document: $from_identity"
            [ "$(yq '.version' "$from_identity")" = 1 ] \
              || fail 14 "$from_identity: unsupported version"
            [ "$(yq '.host' "$from_identity")" = "$host" ] \
              || fail 14 "$from_identity is the identity of $(yq '.host' "$from_identity"), not $host"
            if [ -e age.key ] || [ -e secrets_deploy_key ]; then
              [ "$force" -eq 1 ] || fail 14 "$state already has an identity; --force replaces it"
            fi
            # Documents exported before the keys were renamed say deploy_key and
            # config_key.
            yq '.age_key' "$from_identity" > age.key || fail 14 "cannot write age.key"
            yq '.secrets_deploy_key // .deploy_key' "$from_identity" > secrets_deploy_key \
              || fail 14 "cannot write secrets_deploy_key"
            chmod 600 age.key secrets_deploy_key
            ssh-keygen -y -f secrets_deploy_key > secrets_deploy_key.pub \
              || fail 14 "secrets deploy key in $from_identity is not a valid private key"
            chmod 644 secrets_deploy_key.pub
            # Documents exported before the configuration key existed have none; one
            # is made below.
            if yq -e '.config_deploy_key // .config_key' "$from_identity" >/dev/null 2>&1; then
              yq '.config_deploy_key // .config_key' "$from_identity" > config_deploy_key \
                || fail 14 "cannot write config_deploy_key"
              chmod 600 config_deploy_key
              ssh-keygen -y -f config_deploy_key > config_deploy_key.pub \
                || fail 14 "configuration deploy key in $from_identity is not a valid private key"
              chmod 644 config_deploy_key.pub
            fi
          else
            if [ "$force" -eq 1 ]; then
              rm -f age.key secrets_deploy_key secrets_deploy_key.pub config_deploy_key config_deploy_key.pub
            fi
            if [ ! -e age.key ]; then
              age-keygen -o age.key >/dev/null 2>&1 || fail 14 "age-keygen failed"
              chmod 600 age.key
            fi
            if [ ! -e secrets_deploy_key ]; then
              rm -f secrets_deploy_key.pub
              ssh-keygen -q -t ed25519 -N "" -C "fleet-secrets@$host" -f secrets_deploy_key \
                || fail 14 "ssh-keygen failed"
              chmod 600 secrets_deploy_key
              chmod 644 secrets_deploy_key.pub
            fi
          fi

          # The key that reads the configuration repository, for a fleet whose
          # configuration is private (an SSH conf.fleet.repo.url). Made like the
          # others, and added to an identity made before it existed.
          if [ ! -e config_deploy_key ]; then
            rm -f config_deploy_key.pub
            ssh-keygen -q -t ed25519 -N "" -C "fleet-config@$host" -f config_deploy_key \
              || fail 14 "ssh-keygen failed"
            chmod 600 config_deploy_key
            chmod 644 config_deploy_key.pub
          fi

          # Keys may have been restored without their public half.
          for key in secrets_deploy_key config_deploy_key; do
            if [ ! -e "$key.pub" ]; then
              ssh-keygen -y -f "$key" > "$key.pub" || fail 14 "cannot derive $key.pub"
              chmod 644 "$key.pub"
            fi
          done

          recipient="$(age-keygen -y age.key)" || fail 14 "cannot read the recipient of age.key"
          secrets_pubkey="$(cat secrets_deploy_key.pub)"
          config_pubkey="$(cat config_deploy_key.pub)"

          # Rewritten only when missing or out of step with the key files, so a
          # second run changes nothing; a file from before the keys were
          # renamed is rewritten once.
          if [ ! -f "$host.enroll" ] \
             || [ "$(yq '.age_recipient' "$host.enroll")" != "$recipient" ] \
             || [ "$(yq '.secrets_deploy_pubkey' "$host.enroll")" != "$secrets_pubkey" ] \
             || [ "$(yq '.config_deploy_pubkey' "$host.enroll")" != "$config_pubkey" ]; then
            {
              echo "version: 1"
              echo "host: $host"
              echo "created: $(date -u +%FT%TZ)"
              echo "age_recipient: $recipient"
              echo "secrets_deploy_pubkey: $secrets_pubkey"
              echo "config_deploy_pubkey: $config_pubkey"
            } > "$host.enroll" || fail 14 "cannot write $host.enroll"
            chmod 644 "$host.enroll"
          fi
          ;;

        sync)
          [ -n "$repo" ] || { echo "fleet-secrets-sync: sync needs --repo" >&2; usage; }
          [ -n "$signers" ] || { echo "fleet-secrets-sync: sync needs --signers" >&2; usage; }
          check_state
          cd "$state"
          rename_old_keys
          [ -f age.key ] || fail 14 "no identity: $state/age.key is missing (run init)"
          [ -f secrets_deploy_key ] || fail 14 "no identity: $state/secrets_deploy_key is missing (run init)"
          umask 077

          tmp="$(mktemp -d)"
          trap 'rm -rf "$tmp"' EXIT

          export GIT_SSH_COMMAND="ssh -i $state/secrets_deploy_key -o IdentitiesOnly=yes ${pins.options} -o UserKnownHostsFile=$state/known_hosts -o StrictHostKeyChecking=accept-new"

          # Partial and sparse where the forge allows it; some servers refuse
          # the filter outright, and a plain shallow clone then does the job.
          clone_once() {
            rm -rf "$tmp/clone"
            if git clone --filter=blob:none --no-checkout --depth 1 --branch "$ref" "$repo" "$tmp/clone" 2>"$tmp/err"; then
              return 0
            fi
            if grep -qiE 'filter.*(not supported|unsupported|not recognized)|(not supported|unsupported).*filter' "$tmp/err"; then
              rm -rf "$tmp/clone"
              git clone --depth 1 --branch "$ref" "$repo" "$tmp/clone" 2>"$tmp/err"
              return
            fi
            return 1
          }

          attempt=0
          until clone_once; do
            attempt=$((attempt + 1))
            cat "$tmp/err" >&2
            # Trying again does not change the key the forge shows.
            if grep -q 'Host key verification failed' "$tmp/err"; then
              fail 10 "the forge at $repo did not show the host key this host knows for it, so nothing was fetched; a machine in the middle would look like this"
            fi
            if [ "$attempt" -ge 5 ]; then
              if grep -q 'Permission denied (publickey)' "$tmp/err"; then
                echo "fleet-secrets-sync: the forge rejected this host's deploy key; it has to be enrolled:" >&2
                echo "fleet-secrets-sync: show it with 'fleet-identity' and run 'fleet-secrets host-enroll $host --from FILE' on the maintainer's machine" >&2
              fi
              fail 10 "fetch of $ref from $repo failed after $attempt attempts"
            fi
            sleep $((attempt * 15))
          done

          commit="$(git -C "$tmp/clone" rev-parse HEAD)"
          if ! git -C "$tmp/clone" -c "gpg.ssh.allowedSignersFile=$signers" verify-commit "$commit" >/dev/null 2>&1; then
            fail 11 "commit $commit on $ref is not signed by an allowed signer"
          fi

          # A signature does not stop an old signed commit from being served
          # again, which would bring back secrets that have since been
          # replaced. The commit accepted last, kept in $state/accepted, must
          # be this one or an ancestor of it. The clone holds the tip only, so
          # when the tip has moved its history is fetched first: commits and
          # trees, not the files. Removing the record accepts whatever comes
          # next, for a history rewritten on purpose.
          if [ -s accepted ]; then
            last="$(cat accepted)"
            if [ "$last" != "$commit" ]; then
              if ! git -C "$tmp/clone" fetch --quiet --unshallow origin "$ref" 2>"$tmp/err"; then
                cat "$tmp/err" >&2
                fail 10 "fetch of the history of $ref from $repo failed"
              fi
              if ! git -C "$tmp/clone" merge-base --is-ancestor "$last" "$commit" 2>/dev/null; then
                fail 16 "commit $commit on $ref does not follow $last, the last commit this host accepted; to accept it on purpose, remove $state/accepted"
              fi
            fi
          fi

          git -C "$tmp/clone" sparse-checkout set --no-cone "hosts/$host.yaml" "users/*/$host.yaml"
          git -C "$tmp/clone" checkout --quiet

          mkdir -p snapshots
          new="snapshots/.$commit.new"
          rm -rf "$new" "snapshots/$commit"
          mkdir -p "$new"
          if [ -f "$tmp/clone/hosts/$host.yaml" ]; then
            mkdir "$new/hosts"
            cp -p "$tmp/clone/hosts/$host.yaml" "$new/hosts/$host.yaml"
          fi
          for f in "$tmp"/clone/users/*/"$host.yaml"; do
            [ -f "$f" ] || continue
            u="$(basename "$(dirname "$f")")"
            mkdir -p "$new/users/$u"
            cp -p "$f" "$new/users/$u/$host.yaml"
          done
          find "$new" -type d -exec chmod 700 {} +
          find "$new" -type f -exec chmod 600 {} +
          mv -T "$new" "snapshots/$commit"

          ln -sfn "snapshots/$commit" secrets.tmp && mv -T secrets.tmp secrets
          printf '%s\n' "$commit" > accepted.tmp && mv -f accepted.tmp accepted
          find snapshots -mindepth 1 -maxdepth 1 ! -name "$commit" -exec rm -rf {} +

          if [ -f "snapshots/$commit/hosts/$host.yaml" ]; then
            if ! SOPS_AGE_KEY_FILE="$state/age.key" sops -d --output /dev/null "snapshots/$commit/hosts/$host.yaml" 2>"$tmp/sops-err"; then
              cat "$tmp/sops-err" >&2
              fail 15 "$state/snapshots/$commit/hosts/$host.yaml does not decrypt with this host's key: enrollment incomplete or re-key pending"
            fi
          fi

          echo "$commit"
          ;;

        check)
          [ -n "$config" ] || { echo "fleet-secrets-sync: check needs --config" >&2; usage; }
          check_state
          snapshot="$state/secrets"

          if ! declared="$(nix eval --json "$config#nixosConfigurations.$host.config.sops.secrets" \
                --apply 'builtins.mapAttrs (_: s: { inherit (s) sopsFile key; })')"; then
            echo "fleet-secrets-sync: could not evaluate the secrets of $host in $config" >&2
            exit 1
          fi

          # Keys are looked up as sops-nix does: a / nests (hotspot/wls0 is wls0
          # under hotspot), and an empty key is the whole file.
          # shellcheck disable=SC2016 # $s is yq's variable, not the shell's
          lookup='((strenv(k) | split("/") | .[]) as $s ireduce (.; .[$s])) != null'
          problems=0
          while IFS=$'\t' read -r file key; do
            [ -n "$file" ] || continue
            path="$snapshot/''${file#*/secrets/}"
            if [ ! -f "$path" ]; then
              echo "missing: $path"
              problems=1
            elif [ -n "$key" ] && [ "$(k="$key" yq "$lookup" "$path")" != true ]; then
              echo "missing key $key in $path"
              problems=1
            fi
          done < <(printf '%s' "$declared" | yq -p=json '.[] | .sopsFile + "\t" + .key')

          [ "$problems" -eq 0 ] || exit 12
          ;;
      esac
    '';
  };

  identity = pkgs.writeShellApplication {
    name = "fleet-identity";
    runtimeInputs = with pkgs; [ yq-go qrencode coreutils openssh ];
    text = ''
      usage() {
        cat >&2 <<'EOF'
      usage: fleet-identity [--state DIR] [--qr | --export]

        (no flag)  print the enrollment file
        --qr       print it as a terminal QR code
        --export   print the identity document, private keys included (root only)
      EOF
        exit "''${1:-1}"
      }

      state=/var/lib/fleet
      mode=print

      while [ $# -gt 0 ]; do
        case "$1" in
          --state)   state="''${2:?--state needs a value}"; shift 2 ;;
          --qr)      mode=qr; shift ;;
          --export)  mode="export"; shift ;;
          -h|--help) usage 0 ;;
          *) echo "fleet-identity: unknown option '$1'" >&2; usage ;;
        esac
      done

      enroll="$(find "$state" -maxdepth 1 -name '*.enroll' | sort | head -n 1)"
      [ -n "$enroll" ] || { echo "fleet-identity: no enrollment file in $state" >&2; exit 1; }

      case "$mode" in
        print) cat "$enroll" ;;
        qr)    qrencode -t ANSIUTF8 < "$enroll" ;;
        export)
          if [ "$(id -u)" -ne 0 ]; then
            echo "fleet-identity: --export prints private keys and needs root" >&2
            exit 1
          fi
          echo "version: 1"
          echo "host: $(yq '.host' "$enroll")"
          echo "age_key: |"
          sed 's/^/  /' "$state/age.key"
          # A key init or the sync has not renamed yet goes by an old name.
          for secrets_key in secrets_deploy_key secrets_deploy_ed25519 deploy_ed25519; do
            [ -f "$state/$secrets_key" ] && break
          done
          echo "secrets_deploy_key: |"
          sed 's/^/  /' "$state/$secrets_key"
          for config_key in config_deploy_key config_deploy_ed25519; do
            if [ -f "$state/$config_key" ]; then
              echo "config_deploy_key: |"
              sed 's/^/  /' "$state/$config_key"
              break
            fi
          done
          ;;
      esac
    '';
  };
}
