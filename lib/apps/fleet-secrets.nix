# The maintainer's tool for the fleet's secrets repository.
#
# Run inside a clone of the private secrets repository, whose own checkout
# stays on main. Each host has a branch of its own, host/<host>, holding its
# files and its enrollment record and nothing else. A command for a host works
# in a temporary worktree of that branch, commits there and removes the
# worktree, so the commit holds that command's change and nothing else. There
# is no registry and no .sops.yaml: a file's recipients are in its own sops
# metadata, and the maintainer's recovery recipient is read from the cache
# that fleet setup fills.
{ pkgs }:
pkgs.writeShellApplication {
  name = "fleet-secrets";
  runtimeInputs = with pkgs; [ sops age openssh mkpasswd yq-go git coreutils util-linux ];
  text = ''
    usage() {
      cat >&2 <<'EOF'
    usage: fleet-secrets [--repo DIR] [--recovery FILE] COMMAND ...

      host-enroll HOST --from FILE|-   admit a new host: create its branch host/HOST
      host-rekey  HOST --from FILE|-   point a known host at a new identity
      host-retire HOST                 delete the branch host/HOST
      user-password USER HOST          set the user's password on that host
      user-key USER HOST               generate the user's SSH key on that host

    A command works in a temporary worktree of the host's branch and commits
    there; push the branch afterwards (git push origin host/HOST). A new file
    is encrypted to the host (the recipient in hosts/HOST.yaml) and to your
    recovery recipient, read from
    ''${XDG_STATE_HOME:-$HOME/.local/state}/fleet/recovery.pub, which fleet
    setup writes.

    options:
      --repo DIR        the secrets repository clone, left on main (default: .)
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
    git rev-parse --git-dir >/dev/null 2>&1 || die "$(pwd) is not a git repository; is this the secrets repository?"
    repo="$(pwd)"

    # Host and user names end up in file names and branch names, so keep them
    # to a boring alphabet.
    check_name() {  # check_name KIND VALUE
      case "$2" in
        ""|-*|*[!a-z0-9-]*) die "$1 name '$2' must be lowercase letters, digits and '-'" ;;
        *) ;;
      esac
    }

    # The maintainer's recovery recipient, which fleet setup caches.
    recovery_pub=""
    load_recovery_pub() {
      local f="''${XDG_STATE_HOME:-$HOME/.local/state}/fleet/recovery.pub"
      [ -f "$f" ] || die "no recovery recipient in $f: run fleet setup"
      read -r recovery_pub < "$f" || true
      case "$recovery_pub" in
        age1*[!0-9a-z]*|age1|"") die "$f does not hold an age recipient: run fleet setup" ;;
        age1*) ;;
        *) die "$f does not hold an age recipient: run fleet setup" ;;
      esac
    }

    # The ref that holds the host's branch: the local branch, or else the one
    # fetched from origin; empty when there is neither.
    branch_ref() {  # branch_ref HOST
      if git rev-parse --verify -q "refs/heads/host/$1" >/dev/null; then
        echo "refs/heads/host/$1"
      elif git rev-parse --verify -q "refs/remotes/origin/host/$1" >/dev/null; then
        echo "refs/remotes/origin/host/$1"
      fi
    }

    # The host's own recipient: the one in the sops metadata of its file that
    # is not the recovery recipient.
    host_recipient() {  # host_recipient REF HOST
      local doc r
      local -a others=()
      doc="$(git show "$1:hosts/$2.yaml")" || die "$1 has no hosts/$2.yaml"
      while read -r r; do
        if [ -n "$r" ] && [ "$r" != "$recovery_pub" ]; then others+=("$r"); fi
      done < <(yq '.sops.age[].recipient' <<<"$doc")
      [ "''${#others[@]}" -eq 1 ] \
        || die "hosts/$2.yaml on $1 should be encrypted to the recovery recipient and exactly one other, the host's"
      echo "''${others[0]}"
    }

    # The change builds on the branch's tip and encrypts for the recipients it
    # names, so the tip must be one a trusted key signed, not whatever was
    # pushed last; and the keys it is checked against must be your own copy,
    # outside every checkout, or a pushed commit could add the key that signs
    # it (fleet trust signers). Nor may git run the repository's own hook,
    # which anyone who can push could change.
    require_trusted() {
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

    require_signed() {  # require_signed REF
      git verify-commit "$1" >/dev/null 2>&1 \
        || die "the newest commit of $1 is not signed by a key in gpg.ssh.allowedSignersFile (fleet setup sets it): look at it with git log --show-signature -1 $1 before going on"
    }

    # The temporary worktree the command works in, and the local branch made
    # for it from origin's, which a command that stops early takes away again.
    work=""
    made_branch=""
    keydir=""

    close_work() {
      if [ -n "$work" ]; then
        cd "$repo"
        git worktree remove --force "$work" >/dev/null 2>&1 || rm -rf "$work"
        git worktree prune
        work=""
      fi
      if [ -n "$made_branch" ]; then
        git branch -D -q "$made_branch" >/dev/null 2>&1 || true
        made_branch=""
      fi
    }

    # The private key exists in plain text only in a tmpfs directory, and is
    # shredded however the script ends.
    cleanup() {
      if [ -n "$keydir" ]; then
        shred -u "$keydir/key" "$keydir/key.pub" 2>/dev/null || true
        rmdir "$keydir" 2>/dev/null || true
      fi
      close_work
    }
    trap cleanup EXIT

    open_work() {  # open_work HOST REF — REF empty: the branch is made, empty
      local branch="host/$1"
      work="$(mktemp -d)"
      if [ -z "$2" ]; then
        git worktree add -q --orphan -b "$branch" "$work"
      elif [ "$2" = "refs/heads/$branch" ]; then
        git worktree add -q "$work" "$branch"
      else
        git worktree add -q -b "$branch" "$work" "$2"
        made_branch="$branch"
      fi
      cd "$work"
    }

    # The files of the host on the branch that exist, one per line.
    host_files() {  # host_files HOST
      local f
      for f in "hosts/$1.yaml" users/*/"$1.yaml"; do
        if [ -f "$f" ]; then echo "$f"; fi
      done
    }

    # Creates an empty file encrypted to the host and the recovery recipient.
    create_empty() {  # create_empty PATH HOST-RECIPIENT
      local tmp enc
      mkdir -p "$(dirname "$1")"
      tmp="$(mktemp)"
      echo '{}' > "$tmp"
      enc="$(sops --encrypt --age "$2,$recovery_pub" --input-type yaml --output-type yaml "$tmp")" \
        || { rm -f "$tmp"; die "could not create $1"; }
      rm -f "$tmp"
      printf '%s\n' "$enc" > "$1"
    }

    # The index holds exactly this command's change, since the worktree is new.
    commit() {  # commit MESSAGE
      git commit -q -m "$1"
    }

    # Reads an enrollment file (as written by fleet-secrets-sync init) and
    # sets enrolledRecipient, enrolledDeployKey and enrolledConfigKey (empty
    # in a file written before hosts had a configuration key) after checking
    # it is for the given host. The file itself is kept as enrolledDoc.
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
      enrolledDoc="$doc"
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
      local host source ref old
      parse_host_from "$@"
      require_trusted
      load_recovery_pub
      read_enrollment "$host" "$source"
      ref="$(branch_ref "$host")"
      if [ -n "$ref" ]; then
        old="$(host_recipient "$ref" "$host")"
        if [ "$old" = "$enrolledRecipient" ]; then
          echo "host $host is enrolled already, with this identity; nothing to do"
          return 0
        fi
        die "host '$host' is already enrolled with another identity; use host-rekey"
      fi

      open_work "$host" ""
      create_empty "hosts/$host.yaml" "$enrolledRecipient"
      printf '%s\n' "$enrolledDoc" > enroll

      print_deploy_key "$host"
      git add enroll "hosts/$host.yaml"
      commit "host-enroll: $host"
      close_work
      echo
      echo "Push the branch: git push origin host/$host"
    }

    cmd_host_rekey() {
      local host source ref old f
      local -a files
      parse_host_from "$@"
      require_trusted
      load_recovery_pub
      read_enrollment "$host" "$source"
      ref="$(branch_ref "$host")"
      [ -n "$ref" ] || die "host '$host' is not enrolled; use host-enroll"
      require_signed "$ref"
      old="$(host_recipient "$ref" "$host")"
      [ "$old" != "$enrolledRecipient" ] || die "host '$host' holds this identity already; nothing to re-key"

      open_work "$host" "$ref"
      mapfile -t files < <(host_files "$host")
      for f in "''${files[@]}"; do
        sops rotate -i --rm-age "$old" --add-age "$enrolledRecipient" "$f"
      done
      printf '%s\n' "$enrolledDoc" > enroll

      print_deploy_key "$host"
      echo
      echo "Then delete the host's previous deploy key on the forge."
      git add enroll "''${files[@]}"
      commit "host-rekey: $host"
      close_work
      echo
      echo "Push the branch: git push origin host/$host"
    }

    cmd_host_retire() {
      [ $# -eq 1 ] || usage
      local host="$1" ref f
      check_name host "$host"
      require_trusted
      ref="$(branch_ref "$host")"
      [ -n "$ref" ] || die "host '$host' is not enrolled"

      echo "host/$host, at $(git rev-parse --short "$ref"), holds:"
      git ls-tree -r --name-only "$ref" | while read -r f; do echo "  $f"; done
      if git rev-parse --verify -q "refs/heads/host/$host" >/dev/null; then
        git branch -D -q "host/$host"
        echo "deleted the local branch host/$host"
      else
        echo "there is no local branch host/$host; only origin's copy remains"
      fi

      cat <<EOF

    $host is retired. Still to do:
      1. Delete the branch on the forge, which takes it away from the host:
           git push origin --delete host/$host
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
      local user="$1" host="$2" pw1 pw2 hash ref recipient
      check_name user "$user"
      check_name host "$host"
      require_trusted
      load_recovery_pub
      ref="$(branch_ref "$host")"
      [ -n "$ref" ] || die "host '$host' is not enrolled; use host-enroll first"
      require_signed "$ref"
      recipient="$(host_recipient "$ref" "$host")"

      read -rsp "Password for $user on $host: " pw1; echo >&2
      read -rsp "Again: " pw2; echo >&2
      [ -n "$pw1" ] || die "empty password"
      [ "$pw1" = "$pw2" ] || die "the passwords differ"
      hash="$(printf '%s' "$pw1" | mkpasswd -m yescrypt --stdin)"

      open_work "$host" "$ref"
      if [ ! -f "users/$user/$host.yaml" ]; then
        create_empty "users/$user/$host.yaml" "$recipient"
      fi
      printf '%s' "$hash" | set_from_stdin "users/$user/$host.yaml" password
      git add "users/$user/$host.yaml"
      commit "user-password: $user@$host"
      close_work
      echo "Push the branch: git push origin host/$host"
    }

    cmd_user_key() {
      [ $# -eq 2 ] || usage
      local user="$1" host="$2" base ref
      check_name user "$user"
      check_name host "$host"
      require_trusted
      load_recovery_pub
      ref="$(branch_ref "$host")"
      [ -n "$ref" ] || die "host '$host' is not enrolled; use host-enroll first"
      require_signed "$ref"
      git cat-file -e "$ref:users/$user/$host.yaml" 2>/dev/null \
        || die "users/$user/$host.yaml does not exist on host/$host; run user-password first"

      open_work "$host" "$ref"
      # The private key exists in plain text only in a tmpfs directory, and is
      # shredded however the script ends (cleanup).
      base="/run/user/$(id -u)"
      if [ ! -d "$base" ] || [ ! -w "$base" ]; then base="/dev/shm"; fi
      keydir="$(mktemp -d -p "$base")"

      ssh-keygen -q -t ed25519 -N "" -C "$user@$host" -f "$keydir/key"
      set_from_stdin "users/$user/$host.yaml" ssh_key < "$keydir/key"
      # The public key line ends in a newline of its own; it is not part of it.
      tr -d '\n' < "$keydir/key.pub" | set_from_stdin "users/$user/$host.yaml" ssh_key_pub

      echo
      echo "Public key of $user on $host; register it on the forge:"
      cat "$keydir/key.pub"
      git add "users/$user/$host.yaml"
      commit "user-key: $user@$host"
      close_work
      echo "Push the branch: git push origin host/$host"
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
