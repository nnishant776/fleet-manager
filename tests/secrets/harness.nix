# A reproducible stand-in for the private secrets repository and the
# configuration upstream, for testing the secrets design without any hosting
# service. `up` creates keys, a user-level sshd on port 2222, a bare secrets
# repository (a signed commit on main, and one on the scratch host's branch,
# host/scratch-secrets, with its files) and a bare configuration repository
# with a signed commit; a QEMU guest reaches them at 10.0.2.2 (see README.md).
#
# Everything lives in one directory and `up` only creates what is missing, so
# running it twice changes nothing.
{ pkgs }:
pkgs.writeShellApplication {
  name = "secrets-harness";
  runtimeInputs = with pkgs; [ git openssh sops age mkpasswd coreutils util-linux jq ];
  text = ''
    usage() {
      cat >&2 <<'EOF'
    usage: secrets-harness up|down|status [DIR]

      up      create the test keys, sshd and repositories under DIR (idempotent)
      down    stop the sshd; files are kept
      status  whether the sshd runs, and the last commit of both repositories

    DIR defaults to /tmp/secrets-harness.
    EOF
      exit "''${1:-1}"
    }

    cmd="''${1:-}"
    [ -n "$cmd" ] || usage
    case "$cmd" in
      -h|--help) usage 0 ;;
      up|down|status) ;;
      *) echo "secrets-harness: unknown command '$cmd'" >&2; usage ;;
    esac
    dir="''${2:-/tmp/secrets-harness}"
    port=2222
    me="$(id -un)"

    sshd_running() {
      [ -f "$dir/sshd/pid" ] && pgrep -F "$dir/sshd/pid" >/dev/null 2>&1
    }

    # Git with the maintainer's test signing key, for commits the scratch host
    # verifies against allowed_signers.
    signed_git() {
      git -c gpg.format=ssh -c "user.signingkey=$dir/signing_ed25519" \
        -c commit.gpgsign=true -c user.name=maintainer -c user.email=maintainer@test "$@"
    }

    case "$cmd" in
      down)
        if sshd_running; then
          kill "$(cat "$dir/sshd/pid")"
          echo "secrets-harness: sshd stopped"
        else
          echo "secrets-harness: sshd not running"
        fi
        exit 0 ;;
      status)
        if sshd_running; then
          echo "sshd: running (pid $(cat "$dir/sshd/pid"), port $port)"
        else
          echo "sshd: not running"
        fi
        for repo in secrets config; do
          printf '%s.git: ' "$repo"
          if [ -d "$dir/$repo.git" ]; then
            git -C "$dir/$repo.git" log -1 --format='%h %s (%G?)' 2>/dev/null || echo "no commits"
          else
            echo "missing"
          fi
        done
        exit 0 ;;
    esac

    # ---- up ----
    top="$(git rev-parse --show-toplevel 2>/dev/null)" \
      || { echo "secrets-harness: run this from inside the flake checkout" >&2; exit 1; }
    host=scratch-secrets
    hostdir="$top/hosts/$host"
    [ -d "$hostdir" ] || { echo "secrets-harness: $hostdir does not exist" >&2; exit 1; }

    mkdir -p "$dir/sshd"
    chmod 700 "$dir"

    genkey() {  # genkey PATH COMMENT — an ed25519 key without passphrase, once
      [ -e "$1" ] || ssh-keygen -q -t ed25519 -N "" -C "$2" -f "$1"
    }

    [ -e "$dir/recovery.key" ] || age-keygen -o "$dir/recovery.key" 2>/dev/null
    [ -e "$dir/host.key" ] || age-keygen -o "$dir/host.key" 2>/dev/null
    recovery="$(age-keygen -y "$dir/recovery.key")"
    recipient="$(age-keygen -y "$dir/host.key")"

    genkey "$dir/secrets_deploy_key" "fleet-secrets@$host"
    genkey "$dir/signing_ed25519" "maintainer@test"
    genkey "$dir/support_ed25519" "support@test"
    if [ ! -e "$dir/allowed_signers" ]; then
      echo "maintainer@test $(cut -d' ' -f1,2 "$dir/signing_ed25519.pub")" > "$dir/allowed_signers"
    fi

    # The user-level sshd the scratch host fetches from, authenticating only
    # the host's deploy key.
    genkey "$dir/sshd/ssh_host_ed25519_key" "secrets-harness-sshd"
    cp "$dir/secrets_deploy_key.pub" "$dir/sshd/authorized_keys"
    cat > "$dir/sshd/sshd_config" <<EOF
    Port $port
    HostKey $dir/sshd/ssh_host_ed25519_key
    PidFile $dir/sshd/pid
    AuthorizedKeysFile $dir/sshd/authorized_keys
    PasswordAuthentication no
    ChallengeResponseAuthentication no
    UsePAM no
    StrictModes no
    AllowUsers $me
    EOF
    if ! sshd_running; then
      ${pkgs.openssh}/bin/sshd -f "$dir/sshd/sshd_config" -E "$dir/sshd/log"
    fi

    # The secrets repository: main holds a README; the scratch host's branch,
    # host/$host, one signed commit holding both secret files, encrypted to the
    # host and the recovery key.
    if [ ! -d "$dir/secrets.git" ]; then
      git init -q --bare -b main "$dir/secrets.git"
      rm -rf "$dir/secrets"
      git init -q -b main "$dir/secrets"
      git -C "$dir/secrets" remote add origin "$dir/secrets.git"
      (
        cd "$dir/secrets"
        echo "# Scratch secrets" > README.md
        git add -A
        signed_git commit -q -S -m "Scratch secrets repository"
        git push -q origin main
        git switch -q --orphan "host/$host"
        mkdir -p hosts users/alice users/support
        support_hash="$(mkpasswd -m yescrypt -s <<<support)"
        printf 'support_password: "%s"\n' "$support_hash" > "hosts/$host.yaml"
        ssh-keygen -q -t ed25519 -N "" -C "alice@$host" -f "$dir/alice_ed25519.tmp"
        alice_hash="$(mkpasswd -m yescrypt -s <<<alice)"
        {
          printf 'password: "%s"\n' "$alice_hash"
          echo "ssh_key: |"
          sed 's/^/  /' "$dir/alice_ed25519.tmp"
          printf 'ssh_key_pub: "%s"\n' "$(cat "$dir/alice_ed25519.tmp.pub")"
        } > "users/alice/$host.yaml"
        { echo "authorized_keys: |"; sed 's/^/  /' "$dir/support_ed25519.pub"; } > "users/support/$host.yaml"
        rm -f "$dir/alice_ed25519.tmp" "$dir/alice_ed25519.tmp.pub"
        sops --encrypt --in-place --age "$recipient,$recovery" "hosts/$host.yaml"
        sops --encrypt --in-place --age "$recipient,$recovery" "users/alice/$host.yaml"
        sops --encrypt --in-place --age "$recipient,$recovery" "users/support/$host.yaml"
        git add -A
        signed_git commit -q -S -m "Initial scratch secrets"
        git push -q origin "host/$host"
      )
    fi

    # The files the scratch host reads at evaluation. Rewritten only when they
    # differ, so a second run leaves the tree alone.
    user_url="ssh://$me@10.0.2.2:$port$dir/secrets.git"
    config_url="ssh://$me@10.0.2.2:$port$dir/config.git"
    tmp="$(mktemp)"
    trap 'rm -f "$tmp"' EXIT
    cat > "$tmp" <<EOF
    # Written by secrets-harness; do not edit.
    {
      conf.secrets.repo.url = "$user_url";
      conf.fleet.repo.url = "$config_url";
      conf.fleet.support.keysFromSecrets = true;
      conf.fleet.signing.allowedSignersFile = ./allowed_signers;
    }
    EOF
    if ! cmp -s "$tmp" "$hostdir/harness.nix"; then cp "$tmp" "$hostdir/harness.nix"; fi
    if ! cmp -s "$dir/allowed_signers" "$hostdir/allowed_signers"; then
      cp "$dir/allowed_signers" "$hostdir/allowed_signers"
    fi
    git -C "$top" add -N "hosts/$host/harness.nix" "hosts/$host/allowed_signers"

    # The configuration upstream: this checkout's files (tracked, plus the
    # untracked ones of the scratch host) in one signed commit.
    if [ ! -d "$dir/config.git" ]; then
      git init -q --bare -b main "$dir/config.git"
      work="$(mktemp -d)"
      {
        git -C "$top" ls-files
        (cd "$top" && find "hosts/$host" -type f)
      } | sort -u | while IFS= read -r f; do
        { [ -e "$top/$f" ] || [ -L "$top/$f" ]; } || continue
        mkdir -p "$work/$(dirname "$f")"
        cp -P "$top/$f" "$work/$f"
      done
      git init -q -b main "$work"
      git -C "$work" remote add origin "$dir/config.git"
      (
        cd "$work"
        git add -A
        signed_git commit -q -S -m "Configuration snapshot for the scratch host"
        git push -q origin main
      )
      rm -rf "$work"
    fi

    jq -n \
      --arg dir "$dir" --argjson port "$port" --arg user "$me" \
      --arg recipient "$recipient" --arg deploy "$(cat "$dir/secrets_deploy_key.pub")" \
      --arg secrets "$user_url" --arg config "$config_url" \
      '{ dir: $dir, port: $port, user: $user, secrets_url: $secrets,
         config_url: $config, host_recipient: $recipient, secrets_deploy_pubkey: $deploy,
         files: { recovery_key: ($dir + "/recovery.key"), host_key: ($dir + "/host.key"),
                  signing_key: ($dir + "/signing_ed25519"),
                  allowed_signers: ($dir + "/allowed_signers"),
                  support_key: ($dir + "/support_ed25519") } }'
  '';
}
