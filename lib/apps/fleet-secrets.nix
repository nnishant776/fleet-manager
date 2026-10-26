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

    For a user that hosts/HOST.yaml lists (fleet secrets user-enroll),
    user-password and user-key work on the user's branch, user/USER, instead,
    and a new file is encrypted to the user too.

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

    # The ref that holds the user's branch: the local branch, or else the one
    # fetched from origin; empty when there is neither.
    user_branch_ref() {  # user_branch_ref USER
      if git rev-parse --verify -q "refs/heads/user/$1" >/dev/null; then
        echo "refs/heads/user/$1"
      elif git rev-parse --verify -q "refs/remotes/origin/user/$1" >/dev/null; then
        echo "refs/remotes/origin/user/$1"
      fi
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

    # The hosts that have a branch, local or fetched from origin, one per line.
    list_hosts() {
      git for-each-ref --format='%(refname)' refs/heads/host/ refs/remotes/origin/host/ \
        | sed -e 's|^refs/heads/host/||' -e 's|^refs/remotes/origin/host/||' | sort -u
    }

    # The signing key the host files that list the user hold for them, empty
    # when no host lists them. Every host that lists them has its tip checked (a
    # pushed, unsigned commit must not choose who may sign a user's branch), and
    # they must all hold the same key.
    user_signing_key() {  # user_signing_key USER
      local h r k key="" from=""
      while read -r h; do
        r="$(branch_ref "$h")"
        [ "$(git show "$r:hosts/$h.yaml" | yq ".users[\"$1\"] != null")" = true ] || continue
        require_signed "$r"
        k="$(git show "$r:hosts/$h.yaml" \
          | sops -d --input-type yaml --extract "[\"users\"][\"$1\"][\"signing_key\"]" /dev/stdin)" \
          || die "could not read users.$1 in hosts/$h.yaml"
        if [ -n "$key" ] && [ "$k" != "$key" ]; then
          die "hosts/$from.yaml and hosts/$h.yaml list $1 with different signing keys"
        fi
        key="$k"
        from="$h"
      done < <(list_hosts)
      printf '%s' "$key"
    }

    # The tip of a user's branch must be signed by a key in the maintainer's
    # signers or by the user's own signing key. The user's key goes into a
    # temporary copy of the signers, never into the maintainer's own.
    require_user_signed() {  # require_user_signed REF USER KEY
      local signers tmp rc=0
      if [ -z "$3" ]; then require_signed "$1"; return 0; fi
      signers="$(git config --get gpg.ssh.allowedSignersFile || true)"
      case "$signers" in \~/*) signers="$HOME/''${signers#\~/}" ;; esac
      case "$signers" in ""|/*) ;; *) signers="$(git rev-parse --show-toplevel)/$signers" ;; esac
      tmp="$(mktemp)"
      {
        if [ -n "$signers" ] && [ -f "$signers" ]; then cat "$signers"; echo; fi
        printf '%s namespaces="git" %s\n' "$2" "$(cut -d' ' -f1,2 <<<"$3")"
      } > "$tmp"
      git -c gpg.ssh.allowedSignersFile="$tmp" verify-commit "$1" >/dev/null 2>&1 || rc=$?
      rm -f "$tmp"
      [ "$rc" -eq 0 ] \
        || die "the newest commit of $1 is not signed by a key in gpg.ssh.allowedSignersFile (fleet setup sets it) or by $2's own signing key: look at it with git log --show-signature -1 $1 before going on"
    }

    # A user that hosts/HOST.yaml lists (fleet secrets user-enroll) keeps their
    # files on a branch of their own, user/USER: ref and branch become that
    # branch's, and a new file is also encrypted to the user's recipient. Any
    # other user's files are on the host's branch. Run it with ref set to the
    # host's branch, branch to its name and file_recipient to the host's
    # recipient, after the host's tip is checked.
    use_user_branch() {  # use_user_branch USER HOST
      local listed uref user_recipient key
      listed="$(git show "$ref:hosts/$2.yaml" | yq ".users[\"$1\"] != null")" || die "$ref has no hosts/$2.yaml"
      [ "$listed" = true ] || return 0
      uref="$(user_branch_ref "$1")"
      [ -n "$uref" ] \
        || die "$1 is listed in hosts/$2.yaml, but there is no user/$1 in $repo, local or fetched from origin: fetch it"
      key="$(user_signing_key "$1")"
      require_user_signed "$uref" "$1" "$key"
      user_recipient="$(git show "$ref:hosts/$2.yaml" \
        | sops -d --input-type yaml --output-type json --extract "[\"users\"][\"$1\"]" /dev/stdin \
        | yq '.age_recipient')" || die "could not read users.$1 in hosts/$2.yaml"
      case "$user_recipient" in
        age1|age1*[!0-9a-z]*) die "users.$1 in hosts/$2.yaml holds no age recipient" ;;
        age1*) ;;
        *) die "users.$1 in hosts/$2.yaml holds no age recipient" ;;
      esac
      ref="$uref"
      branch="user/$1"
      file_recipient="$file_recipient,$user_recipient"
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

    open_work() {  # open_work BRANCH REF — REF empty: the branch is made, empty
      local branch="$1"
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
    # Once it holds the commit, a branch made from origin's stays: it is what
    # gets pushed.
    commit() {  # commit MESSAGE
      git commit -q -m "$1"
      made_branch=""
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

      open_work "host/$host" ""
      create_empty "hosts/$host.yaml" "$enrolledRecipient"
      printf '%s\n' "$enrolledDoc" > enroll

      print_deploy_key "$host"
      git add enroll "hosts/$host.yaml"
      commit "host-enroll: $host"
      close_work
      echo
      echo "Push the branch: git push origin host/$host"
    }

    # The users the host's file lists keep their file for the host on their own
    # branch, which the host decrypts too: replace the host's old recipient by
    # its new one there as well, one commit per user. A file that has the new
    # recipient already is left alone, so a run that stopped can be repeated.
    rekey_listed_users() {  # rekey_listed_users HOST HOSTREF OLD NEW
      local u uref key f recips
      while read -r u; do
        [ -n "$u" ] || continue
        uref="$(user_branch_ref "$u")"
        [ -n "$uref" ] \
          || die "$u is listed in hosts/$1.yaml, but there is no user/$u in $repo, local or fetched from origin: fetch it"
        key="$(user_signing_key "$u")"
        require_user_signed "$uref" "$u" "$key"
        f="users/$u/$1.yaml"
        if ! git cat-file -e "$uref:$f" 2>/dev/null; then continue; fi
        recips="$(git show "$uref:$f" | yq '.sops.age[].recipient')"
        if grep -qx "$4" <<<"$recips"; then continue; fi
        open_work "user/$u" "$uref"
        sops rotate -i --rm-age "$3" --add-age "$4" "$f"
        git add "$f"
        commit "host-rekey: $1"
        close_work
        echo "Push the branch: git push origin user/$u"
      done < <(git show "$2:hosts/$1.yaml" | yq '(.users // {}) | keys | .[]')
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

      rekey_listed_users "$host" "$ref" "$old" "$enrolledRecipient"
      open_work "host/$host" "$ref"
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
      local user="$1" host="$2" pw1 pw2 hash ref recipient branch file_recipient
      check_name user "$user"
      check_name host "$host"
      require_trusted
      load_recovery_pub
      ref="$(branch_ref "$host")"
      [ -n "$ref" ] || die "host '$host' is not enrolled; use host-enroll first"
      require_signed "$ref"
      recipient="$(host_recipient "$ref" "$host")"
      branch="host/$host"
      file_recipient="$recipient"
      use_user_branch "$user" "$host"

      read -rsp "Password for $user on $host: " pw1; echo >&2
      read -rsp "Again: " pw2; echo >&2
      [ -n "$pw1" ] || die "empty password"
      [ "$pw1" = "$pw2" ] || die "the passwords differ"
      hash="$(printf '%s' "$pw1" | mkpasswd -m yescrypt --stdin)"

      open_work "$branch" "$ref"
      if [ ! -f "users/$user/$host.yaml" ]; then
        create_empty "users/$user/$host.yaml" "$file_recipient"
      fi
      printf '%s' "$hash" | set_from_stdin "users/$user/$host.yaml" password
      git add "users/$user/$host.yaml"
      commit "user-password: $user@$host"
      close_work
      echo "Push the branch: git push origin $branch"
    }

    cmd_user_key() {
      [ $# -eq 2 ] || usage
      local user="$1" host="$2" base ref branch
      check_name user "$user"
      check_name host "$host"
      require_trusted
      load_recovery_pub
      ref="$(branch_ref "$host")"
      [ -n "$ref" ] || die "host '$host' is not enrolled; use host-enroll first"
      require_signed "$ref"
      branch="host/$host"
      file_recipient=""
      use_user_branch "$user" "$host"
      git cat-file -e "$ref:users/$user/$host.yaml" 2>/dev/null \
        || die "users/$user/$host.yaml does not exist on $branch; run user-password first"

      open_work "$branch" "$ref"
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
      echo "Push the branch: git push origin $branch"
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
