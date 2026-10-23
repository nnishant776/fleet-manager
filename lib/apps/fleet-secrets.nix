# The maintainer's tool for the fleet's secrets repository.
#
# Run inside a clone of the private secrets repository. `fleet.yaml` is the
# registry of recipients and the only thing a human or this tool edits;
# `.sops.yaml` is rendered from it after every change, so the two rules per
# host cannot drift from the list of hosts. Every command that
# changes files commits, and refuses to start on a dirty tree so that the
# commit holds that command's change and nothing else.
{ pkgs }:
pkgs.writeShellApplication {
  name = "fleet-secrets";
  runtimeInputs = with pkgs; [ sops age openssh mkpasswd yq-go git coreutils util-linux ];
  text = ''
    usage() {
      cat >&2 <<'EOF'
    usage: fleet-secrets [--repo DIR] [--recovery FILE] COMMAND ...

      host-enroll HOST --from FILE|-   admit a new host from its enrollment file
      host-rekey  HOST --from FILE|-   point a known host at a new identity
      host-retire HOST                 remove a host and its files
      user-password USER HOST          set the user's password on that host
      user-key USER HOST               generate the user's SSH key on that host

    options:
      --repo DIR        the secrets repository checkout (default: .)
      --recovery FILE   passphrase-encrypted recovery identity (age -p output);
                        decrypted in memory only. Without it, sops reads
                        SOPS_AGE_KEY_FILE / SOPS_AGE_KEY as usual.
      -h, --help        show this text
    EOF
      exit "''${1:-1}"
    }

    die() {
      echo "fleet-secrets: $*" >&2
      exit 1
    }

    repo="."
    recovery=""

    while [ $# -gt 0 ]; do
      case "$1" in
        --repo)      repo="''${2:?--repo needs a value}"; shift 2 ;;
        --recovery)  recovery="''${2:?--recovery needs a value}"; shift 2 ;;
        -h|--help)   usage 0 ;;
        -*)          echo "fleet-secrets: unknown option '$1'" >&2; usage ;;
        *)           break ;;
      esac
    done

    [ $# -gt 0 ] || usage 0
    command="$1"
    shift

    # The recovery identity lives in a variable of this process only, so it is
    # never written to disk; age prompts for its passphrase. It is not
    # exported: only sops gets it, never git, whose hooks come from the
    # repository, nor ssh.
    if [ -n "$recovery" ]; then
      recovery_key="$(age -d "$recovery")" || die "could not decrypt $recovery"
      sops() { SOPS_AGE_KEY="$recovery_key" command sops "$@"; }
    fi

    cd "$repo" || die "no such directory: $repo"
    [ -f fleet.yaml ] || die "no fleet.yaml in $(pwd); is this the secrets repository?"

    # Host and user names end up in file names and in regular expressions of
    # .sops.yaml, so keep them to a boring alphabet.
    check_name() {  # check_name KIND VALUE
      case "$2" in
        ""|-*|*[!a-z0-9-]*) die "$1 name '$2' must be lowercase letters, digits and '-'" ;;
        *) ;;
      esac
    }

    known_host() {  # known_host HOST — is it in fleet.yaml?
      [ "$(h="$1" yq '.hosts | has(strenv(h))' fleet.yaml)" = true ]
    }

    # Untracked files are not a reason to refuse: the commands create some.
    # The change builds on the newest commit and encrypts for the recipients
    # it names, so that commit must be one a trusted key signed, not whatever
    # was pushed last; and the keys it is checked against must be your own copy,
    # outside every checkout, or a pushed commit could add the key that signs
    # it (fleet trust signers). Nor may git run the repository's own hook,
    # which anyone who can push could change.
    require_clean() {
      if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
        die "the working tree has uncommitted changes; commit or discard them first"
      fi
      git verify-commit HEAD >/dev/null 2>&1 \
        || die "the newest commit is not signed by a key in gpg.ssh.allowedSignersFile (fleet setup sets it): look at it with git log --show-signature -1 before going on"
      local signers
      signers="$(git config --get gpg.ssh.allowedSignersFile || true)"
      case "$signers" in \~/*) signers="$HOME/''${signers#\~/}" ;; esac
      case "$signers" in ""|/*) ;; *) signers="$(git rev-parse --show-toplevel)/$signers" ;; esac
      if [ -n "$signers" ] && git -C "$(dirname "$signers")" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        die "git checks signatures against $signers, which is inside a checkout, so a pushed commit could add the key that signs it: run fleet setup, which points it at your own copy of the keys"
      fi
      [ "$(git config --local --default "" --get core.hooksPath)" != .githooks ] \
        || die "git runs the repository's own hook (core.hooksPath is .githooks): run fleet setup, which installs a copy you have read in its place"
    }

    # Renders .sops.yaml from fleet.yaml: literal recipients, no anchors, two
    # rules per host (its own file and its users' files) and none per user.
    write_sops_yaml() {
      local maintainer host recipient
      maintainer="$(yq '.maintainer' fleet.yaml)"
      case "$maintainer" in age1*) ;; *) die "fleet.yaml has no maintainer recipient" ;; esac
      {
        echo "# Generated by fleet-secrets from fleet.yaml. Do not edit."
        if [ -z "$(yq '.hosts | keys | .[]' fleet.yaml)" ]; then
          echo "creation_rules: []"
        else
          echo "creation_rules:"
        fi
        yq '.hosts | keys | .[]' fleet.yaml | while read -r host; do
          recipient="$(h="$host" yq '.hosts[strenv(h)]' fleet.yaml)"
          echo "  - path_regex: ^hosts/$host\\.yaml\$"
          echo "    key_groups:"
          echo "      - age: [\"$maintainer\", \"$recipient\"]"
          echo "  - path_regex: ^users/[^/]+/$host\\.yaml\$"
          echo "    key_groups:"
          echo "      - age: [\"$maintainer\", \"$recipient\"]"
        done
      } > .sops.yaml
    }

    # Re-encrypts the given files to the recipients their rules now name.
    update_keys() {
      local f
      for f in "$@"; do
        sops updatekeys -y "$f"
      done
    }

    # The files the host's rules cover that exist, one per line.
    host_files() {  # host_files HOST
      local f
      for f in "hosts/$1.yaml" users/*/"$1.yaml"; do
        if [ -f "$f" ]; then echo "$f"; fi
      done
    }

    all_files() {
      local f
      for f in hosts/*.yaml users/*/*.yaml; do
        if [ -f "$f" ]; then echo "$f"; fi
      done
    }

    # Creates an empty encrypted file under the rules just rendered.
    create_empty() {  # create_empty PATH
      local tmp enc
      mkdir -p "$(dirname "$1")"
      tmp="$(mktemp)"
      echo '{}' > "$tmp"
      enc="$(sops --encrypt --input-type yaml --output-type yaml \
        --filename-override "$1" "$tmp")" || { rm -f "$tmp"; die "could not create $1"; }
      rm -f "$tmp"
      printf '%s\n' "$enc" > "$1"
    }

    # The index holds exactly this command's change, since the tree was clean.
    commit() {  # commit MESSAGE
      git commit -q -m "$1"
    }

    # Reads an enrollment file (as written by fleet-secrets-sync init) and
    # sets enrolledRecipient, enrolledDeployKey and enrolledConfigKey (empty
    # in a file written before hosts had a configuration key) after checking
    # it is for the given host.
    read_enrollment() {  # read_enrollment HOST SOURCE
      local doc version host
      if [ "$2" = "-" ]; then
        doc="$(cat)"
      else
        [ -f "$2" ] || die "no such file: $2"
        doc="$(cat "$2")"
      fi
      version="$(yq '.version' <<<"$doc")"
      host="$(yq '.host' <<<"$doc")"
      enrolledRecipient="$(yq '.age_recipient' <<<"$doc")"
      # Files written before the keys were renamed say deploy_pubkey.
      enrolledDeployKey="$(yq '.secrets_deploy_pubkey // .deploy_pubkey' <<<"$doc")"
      enrolledConfigKey="$(yq '.config_deploy_pubkey // ""' <<<"$doc")"
      [ "$version" = 1 ] || die "unsupported enrollment file version '$version'"
      [ "$host" = "$1" ] || die "enrollment file is for host '$host', not '$1'"
      case "$enrolledRecipient" in
        age1|age1*[!0-9a-z]*) die "malformed age_recipient in the enrollment file" ;;
        age1*) ;;
        *) die "malformed age_recipient in the enrollment file" ;;
      esac
      case "$enrolledDeployKey" in
        "ssh-ed25519 "*) ;;
        *) die "secrets_deploy_pubkey in the enrollment file is not an ssh-ed25519 key" ;;
      esac
      case "$enrolledConfigKey" in
        ""|"ssh-ed25519 "*) ;;
        *) die "config_deploy_pubkey in the enrollment file is not an ssh-ed25519 key" ;;
      esac
    }

    # Parses "HOST --from SOURCE" into host and source.
    parse_host_from() {  # parse_host_from ARGS...
      if [ $# -ne 3 ] || [ "$2" != "--from" ]; then usage; fi
      host="$1"
      source="$3"
      check_name host "$host"
    }

    print_deploy_key() {  # print_deploy_key HOST
      echo
      echo "Secrets deploy key of $1:"
      echo "  $enrolledDeployKey"
      echo
      echo "Register it read-only on this repository (the web page, Settings -> Deploy keys,"
      echo "is the safer route), or with:"
      echo "  gh repo deploy-key add <file holding the key above> --title $1"
      if [ -n "$enrolledConfigKey" ]; then
        echo
        echo "Configuration deploy key of $1:"
        echo "  $enrolledConfigKey"
        echo
        echo "If its configuration repository is private (an SSH conf.fleet.repo.url),"
        echo "register this one read-only there, the same way:"
        echo "  gh repo deploy-key add <file holding the key above> --repo <owner>/<configuration repository> --title $1"
      fi
    }

    cmd_host_enroll() {
      local host source
      local -a files
      parse_host_from "$@"
      require_clean
      if known_host "$host"; then die "host '$host' is already enrolled; use host-rekey"; fi
      [ ! -e "hosts/$host.yaml" ] || die "hosts/$host.yaml exists already"
      read_enrollment "$host" "$source"

      h="$host" r="$enrolledRecipient" yq -i '.hosts[strenv(h)] = strenv(r)' fleet.yaml
      write_sops_yaml
      # Files of this host that exist already (a user file written before the
      # host was enrolled) are now covered by the new rules.
      mapfile -t files < <(host_files "$host")
      update_keys "''${files[@]}"
      create_empty "hosts/$host.yaml"

      print_deploy_key "$host"
      git add fleet.yaml .sops.yaml "hosts/$host.yaml" "''${files[@]}"
      commit "host-enroll: $host"
    }

    cmd_host_rekey() {
      local host source
      local -a files
      parse_host_from "$@"
      require_clean
      known_host "$host" || die "host '$host' is not enrolled; use host-enroll"
      read_enrollment "$host" "$source"

      h="$host" r="$enrolledRecipient" yq -i '.hosts[strenv(h)] = strenv(r)' fleet.yaml
      write_sops_yaml
      mapfile -t files < <(host_files "$host")
      update_keys "''${files[@]}"

      print_deploy_key "$host"
      echo
      echo "Then delete the host's previous deploy key on the forge."
      git add fleet.yaml .sops.yaml "''${files[@]}"
      commit "host-rekey: $host"
    }

    cmd_host_retire() {
      [ $# -eq 1 ] || usage
      local host="$1" f
      local -a files
      check_name host "$host"
      require_clean
      known_host "$host" || die "host '$host' is not enrolled"

      h="$host" yq -i 'del(.hosts[strenv(h)])' fleet.yaml
      write_sops_yaml
      mapfile -t files < <(host_files "$host")
      for f in "''${files[@]}"; do
        git rm -q -f "$f"
      done
      # Other files never included this host; re-encrypting them is a safety net.
      mapfile -t files < <(all_files)
      update_keys "''${files[@]}"

      git add fleet.yaml .sops.yaml "''${files[@]}"
      commit "host-retire: $host"

      cat <<EOF

    $host is retired. Still to do:
      1. Push this commit.
      2. Delete the host's deploy key on the forge; that takes effect at once.
      3. Rotate what the host could decrypt, which is only its own files:
         its users' passwords and SSH keys on $host (revoke their public keys
         on the forge now) and whatever was in hosts/$host.yaml.
    EOF
    }

    # Sets one key of an encrypted file to the contents of stdin, as a JSON
    # string, so that secrets never appear in a process listing.
    set_from_stdin() {  # set_from_stdin FILE KEY < value
      local json
      json="$(v="$(cat)" yq -n -o=json 'strenv(v)')"
      printf '%s' "$json" | sops set --value-stdin "$1" "[\"$2\"]"
    }

    cmd_user_password() {
      [ $# -eq 2 ] || usage
      local user="$1" host="$2" pw1 pw2 hash
      check_name user "$user"
      check_name host "$host"
      require_clean
      known_host "$host" || die "host '$host' is not enrolled; use host-enroll first"

      read -rsp "Password for $user on $host: " pw1; echo >&2
      read -rsp "Again: " pw2; echo >&2
      [ -n "$pw1" ] || die "empty password"
      [ "$pw1" = "$pw2" ] || die "the passwords differ"
      hash="$(printf '%s' "$pw1" | mkpasswd -m yescrypt --stdin)"

      if [ ! -f "users/$user/$host.yaml" ]; then
        create_empty "users/$user/$host.yaml"
      fi
      printf '%s' "$hash" | set_from_stdin "users/$user/$host.yaml" password
      git add "users/$user/$host.yaml"
      commit "user-password: $user@$host"
    }

    cmd_user_key() {
      [ $# -eq 2 ] || usage
      local user="$1" host="$2" base
      check_name user "$user"
      check_name host "$host"
      require_clean
      known_host "$host" || die "host '$host' is not enrolled; use host-enroll first"
      [ -f "users/$user/$host.yaml" ] \
        || die "users/$user/$host.yaml does not exist; run user-password first"

      # The private key exists in plain text only in a tmpfs directory, and is
      # shredded however the script ends.
      base="/run/user/$(id -u)"
      if [ ! -d "$base" ] || [ ! -w "$base" ]; then base="/dev/shm"; fi
      keydir="$(mktemp -d -p "$base")"
      trap 'shred -u "$keydir/key" "$keydir/key.pub" 2>/dev/null; rmdir "$keydir" 2>/dev/null' EXIT

      ssh-keygen -q -t ed25519 -N "" -C "$user@$host" -f "$keydir/key"
      set_from_stdin "users/$user/$host.yaml" ssh_key < "$keydir/key"
      # The public key line ends in a newline of its own; it is not part of it.
      tr -d '\n' < "$keydir/key.pub" | set_from_stdin "users/$user/$host.yaml" ssh_key_pub

      echo
      echo "Public key of $user on $host; register it on the forge:"
      cat "$keydir/key.pub"
      git add "users/$user/$host.yaml"
      commit "user-key: $user@$host"
    }

    case "$command" in
      host-enroll)    cmd_host_enroll "$@" ;;
      host-rekey)     cmd_host_rekey "$@" ;;
      host-retire)    cmd_host_retire "$@" ;;
      user-password)  cmd_user_password "$@" ;;
      user-key)       cmd_user_key "$@" ;;
      *)              echo "fleet-secrets: unknown command '$command'" >&2; usage ;;
    esac
  '';
}
