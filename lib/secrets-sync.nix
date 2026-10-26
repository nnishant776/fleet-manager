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
      git openssh age sops yq-go coreutils util-linux findutils gnugrep gawk nix
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

          # One bare repository for the whole run, a ref per branch. Nothing is
          # checked out: the files are read with git show.
          g() { git --git-dir="$tmp/repo" "$@"; }
          init_repo() {
            rm -rf "$tmp/repo"
            git init -q --bare "$tmp/repo"
            g remote add origin "$repo"
          }
          init_repo
          fetched=0
          # Partial where the forge allows it; some servers refuse the filter
          # outright, and a plain shallow fetch then does the job.
          filter=(--filter=blob:none)
          fetch_once() {
            if g fetch --quiet "''${filter[@]}" "$@" 2>"$tmp/err"; then
              return 0
            fi
            if [ "''${#filter[@]}" -gt 0 ] && grep -qiE 'filter.*(not supported|unsupported|not recognized)|(not supported|unsupported).*filter' "$tmp/err"; then
              filter=()
              # The refused filter may have left its marks in the repository.
              if [ "$fetched" -eq 0 ]; then
                init_repo
              fi
              g fetch --quiet "$@" 2>"$tmp/err"
              return
            fi
            return 1
          }
          fetch_retry() {  # fetch_retry WHAT ARGS...: git fetch, with retries
            local what="$1" attempt=0
            shift
            until fetch_once "$@"; do
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
                fail 10 "fetch of $what from $repo failed after $attempt attempts"
              fi
              sleep $((attempt * 15))
            done
            fetched=1
          }

          fetch_retry "$ref" --depth 1 origin "+refs/heads/$ref:refs/fleet/host"

          commit="$(g rev-parse refs/fleet/host)"
          if ! g -c "gpg.ssh.allowedSignersFile=$signers" verify-commit "$commit" >/dev/null 2>&1; then
            fail 11 "commit $commit on $ref is not signed by an allowed signer"
          fi

          # The rollback records, one per branch, in $state/accepted. They were
          # one file, the host's; it moves to accepted/host once.
          if [ ! -e accepted ] && [ -d accepted.new ]; then
            mv -T accepted.new accepted
          fi
          if [ -f accepted ]; then
            rm -rf accepted.new
            mkdir accepted.new
            mv accepted accepted.new/host
            mv -T accepted.new accepted
          fi
          mkdir -p accepted

          # A signature does not stop an old signed commit from being served
          # again, which would bring back secrets that have since been
          # replaced. The commit accepted last, kept in $state/accepted/host,
          # must be this one or an ancestor of it. The repository holds the tip
          # only, so when the tip has moved its history is fetched first:
          # commits and trees, not the files. Removing the record accepts
          # whatever comes next, for a history rewritten on purpose.
          if [ -s accepted/host ]; then
            last="$(cat accepted/host)"
            if [ "$last" != "$commit" ]; then
              if ! g fetch --quiet "''${filter[@]}" --depth 2147483647 origin "+refs/heads/$ref:refs/fleet/host" 2>"$tmp/err"; then
                cat "$tmp/err" >&2
                fail 10 "fetch of the history of $ref from $repo failed"
              fi
              if [ "$(g rev-parse refs/fleet/host)" != "$commit" ]; then
                fail 10 "$ref on $repo moved while its history was fetched; the next run starts again"
              fi
              if ! g merge-base --is-ancestor "$last" "$commit" 2>/dev/null; then
                fail 16 "commit $commit on $ref does not follow $last, the last commit this host accepted; to accept it on purpose, remove $state/accepted/host"
              fi
            fi
          fi

          # The host's file, and from it the users this host serves from
          # their own branches. The key is encrypted; nothing else of the
          # file is read here. No such key: no such users.
          hostfile="$tmp/host.yaml"
          have_host_file=0
          if [ -n "$(g ls-tree --name-only "$commit" -- "hosts/$host.yaml")" ]; then
            g show "$commit:hosts/$host.yaml" > "$hostfile"
            have_host_file=1
          fi
          own="$(age-keygen -y "$state/age.key")" || fail 14 "cannot read the recipient of $state/age.key"
          listed=()
          if [ "$have_host_file" -eq 1 ] && [ "$(yq '.users != null' "$hostfile")" = true ]; then
            if SOPS_AGE_KEY_FILE="$state/age.key" sops -d --extract '["users"]' "$hostfile" > "$tmp/users.yaml" 2>"$tmp/sops-err" \
               && mapfile -t listed < <(yq 'keys | .[]' "$tmp/users.yaml"); then
              :
            else
              # The check at the end says so with its own message and code.
              cat "$tmp/sops-err" >&2
              echo "fleet-secrets-sync: the users of $host cannot be read; none is taken from its own branch this time" >&2
              listed=()
            fi
          fi

          # The recovery recipient is the other one in the host's file.
          recovery=""
          if [ "$have_host_file" -eq 1 ]; then
            recovery="$(yq '(.sops.age // [])[].recipient' "$hostfile" | grep -vxF "$own" || true)"
          fi
          recovery_problem=""
          if [ -z "$recovery" ]; then
            recovery_problem="hosts/$host.yaml has no recipient besides this host's own, so a user's file cannot be checked"
          elif [ "$(printf '%s\n' "$recovery" | grep -c .)" -ne 1 ]; then
            recovery_problem="hosts/$host.yaml has more than one recipient besides this host's own, so a user's file cannot be checked"
          fi

          problems="$tmp/problems"
          : > "$problems"
          problem() {  # problem USER REASON
            printf '%s %s\n' "$1" "$2" >> "$problems"
            echo "fleet-secrets-sync: user $1: $2" >&2
          }

          # Which of the listed users have a branch, and a fetch of those.
          declare -A listed_set=() has_branch=()
          valid=()
          for u in "''${listed[@]}"; do
            if [[ ! "$u" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
              problem "$(printf '%s' "$u" | tr -c 'a-z0-9-' '?')" "is not a valid user name"
              continue
            fi
            listed_set[$u]=1
            valid+=("$u")
          done
          if [ "''${#valid[@]}" -gt 0 ]; then
            if ! heads="$(g ls-remote origin 'refs/heads/user/*' 2>"$tmp/err")"; then
              cat "$tmp/err" >&2
              fail 10 "listing the user branches of $repo failed"
            fi
            refspecs=()
            for u in "''${valid[@]}"; do
              if printf '%s\n' "$heads" | cut -f2 | grep -qxF "refs/heads/user/$u"; then
                has_branch[$u]=1
                refspecs+=("+refs/heads/user/$u:refs/fleet/user/$u")
              fi
            done
            if [ "''${#refspecs[@]}" -gt 0 ]; then
              fetch_retry "the user branches" --depth 1 origin "''${refspecs[@]}"
            fi
          fi

          # What a user's file has to be before the host takes it: the tip of
          # the user's branch signed by the user's key or an allowed signer, not
          # older than the one accepted last, the file encrypted to exactly the
          # user, this host and the recovery key, and a password that is a
          # hash. Prints why not, in one line, when it is not.
          pw_re='^(\$[0-9a-z]+\$[^[:space:]]+|![^[:space:]]*)$'
          check_user() {  # check_user USER
            local u="$1" key recipient tip last allowed file have want pw
            if [ -z "''${has_branch[$u]:-}" ]; then
              echo "has no branch user/$u"
              return 1
            fi
            key="$(U="$u" yq '.[strenv(U)].signing_key // ""' "$tmp/users.yaml" | head -n 1 | cut -d' ' -f1,2)"
            recipient="$(U="$u" yq '.[strenv(U)].age_recipient // ""' "$tmp/users.yaml" | head -n 1)"
            if [[ ! "$key" =~ ^[a-z0-9@.-]+\ [A-Za-z0-9+/=]+$ ]]; then
              echo "is listed without a signing key this host can use"
              return 1
            fi
            if [[ ! "$recipient" =~ ^age1[a-z0-9]+$ ]]; then
              echo "is listed without an age recipient"
              return 1
            fi
            if [ -n "$recovery_problem" ]; then
              echo "$recovery_problem"
              return 1
            fi

            tip="$(g rev-parse "refs/fleet/user/$u")"
            allowed="$tmp/allowed-$u"
            { cat "$signers"; printf '\nuser-%s namespaces="git" %s\n' "$u" "$key"; } > "$allowed"
            if ! g -c "gpg.ssh.allowedSignersFile=$allowed" verify-commit "$tip" >/dev/null 2>&1; then
              echo "commit $tip on user/$u is not signed by the user's key or an allowed signer"
              return 1
            fi

            if [ -s "accepted/user-$u" ]; then
              last="$(cat "accepted/user-$u")"
              if [ "$last" != "$tip" ]; then
                if ! g fetch --quiet "''${filter[@]}" --depth 2147483647 origin "+refs/heads/user/$u:refs/fleet/user/$u" 2>"$tmp/err"; then
                  echo "the history of user/$u could not be fetched"
                  return 1
                fi
                if [ "$(g rev-parse "refs/fleet/user/$u")" != "$tip" ]; then
                  echo "user/$u moved while its history was fetched"
                  return 1
                fi
                if ! g merge-base --is-ancestor "$last" "$tip" 2>/dev/null; then
                  echo "commit $tip on user/$u does not follow $last, the last commit accepted from it; to accept it on purpose, remove $state/accepted/user-$u"
                  return 1
                fi
              fi
            fi

            file="users/$u/$host.yaml"
            if [ -z "$(g ls-tree --name-only "$tip" -- "$file")" ] || ! g show "$tip:$file" > "$tmp/user-$u.yaml" 2>/dev/null; then
              echo "user/$u holds no $file"
              return 1
            fi
            have="$(yq '(.sops.age // [])[].recipient' "$tmp/user-$u.yaml" | LC_ALL=C sort -u)"
            want="$(printf '%s\n' "$recipient" "$own" "$recovery" | LC_ALL=C sort -u)"
            if [ "$have" != "$want" ] \
               || [ "$(yq '[.sops.pgp, .sops.kms, .sops.gcp_kms, .sops.azure_kv, .sops.hc_vault] | map(select(. != null and length > 0)) | length' "$tmp/user-$u.yaml")" != 0 ]; then
              echo "$file is not encrypted to exactly the user, this host and the recovery key"
              return 1
            fi
            if [ "$(yq '.password != null' "$tmp/user-$u.yaml")" = true ]; then
              if ! pw="$(SOPS_AGE_KEY_FILE="$state/age.key" sops -d --extract '["password"]' "$tmp/user-$u.yaml" 2>/dev/null)"; then
                echo "the password in $file does not decrypt with this host's key"
                return 1
              fi
              if [[ ! "$pw" =~ $pw_re ]]; then
                echo "the password in $file is not a password hash"
                return 1
              fi
            fi
            printf '%s\n' "$tip" > "$tmp/user-$u.commit"
          }

          # What the snapshot in use records for a user whose file is kept.
          kept_line() {  # kept_line USER
            local rest=""
            if [ -f secrets/manifest ]; then
              # shellcheck disable=SC2016 # $1 and $2 are awk's fields, not the shell's
              rest="$(awk -v u="$1" '$1 == "user" && $2 == u { $1 = ""; $2 = ""; sub(/^ +/, ""); print }' secrets/manifest)"
            fi
            case "$rest" in
              "")      rest="kept unknown" ;;
              kept\ *) ;;
              *)       rest="kept $rest" ;;
            esac
            echo "user $1 $rest"
          }

          mkdir -p snapshots
          stage=snapshots/.new
          rm -rf "$stage"
          mkdir -p "$stage"
          lines="$tmp/user-lines"
          : > "$lines"
          if [ "$have_host_file" -eq 1 ]; then
            mkdir "$stage/hosts"
            cp "$hostfile" "$stage/hosts/$host.yaml"
          fi

          take() {  # take USER FILE: the file goes into the snapshot being made
            mkdir -p "$stage/users/$1"
            cp "$2" "$stage/users/$1/$host.yaml"
          }

          taken=()
          for u in "''${valid[@]}"; do
            if reason="$(check_user "$u")"; then
              take "$u" "$tmp/user-$u.yaml"
              echo "user $u $(cat "$tmp/user-$u.commit")" >> "$lines"
              taken+=("$u")
            else
              problem "$u" "$reason"
              # Whatever the snapshot in use holds for it stays.
              if [ -f "secrets/users/$u/$host.yaml" ]; then
                take "$u" "secrets/users/$u/$host.yaml"
                kept_line "$u" >> "$lines"
              fi
            fi
          done

          # The users who are not listed have their files on the host's branch.
          # A listed user's file there, if any, is not read.
          while IFS= read -r path; do
            [[ "$path" =~ ^users/([^/]+)/([^/]+)$ ]] || continue
            [ "''${BASH_REMATCH[2]}" = "$host.yaml" ] || continue
            u="''${BASH_REMATCH[1]}"
            [ -z "''${listed_set[$u]:-}" ] || continue
            if [[ "$u" =~ ^(\.|\.\.)$ ]]; then continue; fi
            g show "$commit:$path" > "$tmp/branch-user.yaml"
            take "$u" "$tmp/branch-user.yaml"
            echo "user $u host-branch" >> "$lines"
          done < <(g ls-tree -r --name-only "$commit" -- users)

          # The manifest names what the snapshot was made from, and its hash
          # names the snapshot: a run that changes nothing makes no new one.
          { echo "host $commit"; LC_ALL=C sort "$lines"; } > "$stage/manifest"
          find "$stage" -type d -exec chmod 700 {} +
          find "$stage" -type f -exec chmod 600 {} +
          name="$(sha256sum "$stage/manifest" | cut -d' ' -f1)"
          if [ -d "snapshots/$name" ]; then
            rm -rf "$stage"
          else
            mv -T "$stage" "snapshots/$name"
          fi

          if [ "$(readlink secrets 2>/dev/null || true)" != "snapshots/$name" ]; then
            ln -sfn "snapshots/$name" secrets.tmp && mv -T secrets.tmp secrets
          fi
          printf '%s\n' "$commit" > accepted/host.tmp && mv -f accepted/host.tmp accepted/host
          for u in "''${taken[@]}"; do
            printf '%s\n' "$(cat "$tmp/user-$u.commit")" > "accepted/user-$u.tmp" && mv -f "accepted/user-$u.tmp" "accepted/user-$u"
          done
          # Rewritten every run; the notice at the machine reads it.
          if [ -s "$problems" ]; then
            install -m 0644 "$problems" user-problems.tmp && mv -f user-problems.tmp user-problems
          else
            rm -f user-problems
          fi
          find snapshots -mindepth 1 -maxdepth 1 ! -name "$name" -exec rm -rf {} +

          if [ -f "snapshots/$name/hosts/$host.yaml" ]; then
            if ! SOPS_AGE_KEY_FILE="$state/age.key" sops -d --output /dev/null "snapshots/$name/hosts/$host.yaml" 2>"$tmp/sops-err"; then
              cat "$tmp/sops-err" >&2
              fail 15 "$state/snapshots/$name/hosts/$host.yaml does not decrypt with this host's key: enrollment incomplete or re-key pending"
            fi
          fi

          echo "$name"
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
