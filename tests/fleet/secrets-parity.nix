# Runs the shell fleet-secrets (lib/apps/fleet-secrets.nix) and its Go port
# (`fleet secrets …`) through the same commands, on two copies of one scratch
# repository, and checks after every command that they left it the same:
# - the same branches (one host/<name> per host, and user/<name> for users the
#   hosts list) and none left over, the same
#   tracked files on each and the same commit messages, every tip signed, and
#   the checkout still on main with no worktree left behind;
# - every encrypted file decrypting to the same values, for the same
#   recipients, which are the host's and the recovery key's and no others.
# Password hashes and SSH keys are random, so those are checked for what they
# must be instead: the hash verifies the password, and the key pair matches,
# with the same comment. After the re-key and the retirement it also checks
# who can still decrypt what. A last part enrolls users (the port only) and
# runs the commands for a listed user, whose files are on user/<name>.
# Run it with `nix build .#checks.x86_64-linux.fleet-secrets-parity`.
{ pkgs, fleet }:
let
  shellTool = import ../../lib/apps/fleet-secrets.nix { inherit pkgs; };
in
pkgs.runCommand "fleet-secrets-parity" {
  nativeBuildInputs = with pkgs; [ git sops age yq-go openssh mkpasswd perl jq diffutils ];
} ''
  set -euo pipefail
  export HOME=$PWD/home
  mkdir -p "$HOME"
  git config --global user.name test
  git config --global user.email test@example.com
  git config --global init.defaultBranch main
  # Every commit is signed, as in the real repository: both tools refuse to
  # build on one that is not.
  ssh-keygen -q -t ed25519 -N "" -f signing
  echo "test namespaces=\"git\" $(cut -d' ' -f1,2 signing.pub)" > allowed_signers
  git config --global gpg.format ssh
  git config --global user.signingKey "$PWD/signing"
  git config --global commit.gpgSign true
  git config --global gpg.ssh.allowedSignersFile "$PWD/allowed_signers"

  # The maintainer, two hosts, and a second identity for the first host.
  age-keygen -o maint.key 2>/dev/null
  for id in alpha beta alpha2; do
    age-keygen -o $id.key 2>/dev/null
    ssh-keygen -q -t ed25519 -N "" -f $id.deploy
  done
  # FIELD is secrets_deploy_pubkey, or deploy_pubkey as files written before
  # the keys were renamed say; both tools take either.
  enrollment() {  # enrollment ID HOST [FIELD]
    printf 'version: 1\nhost: %s\ncreated: 2026-10-18T00:00:00Z\nage_recipient: %s\n%s: %s fleet-deploy@%s\n' \
      "$2" "$(age-keygen -y $1.key)" "''${3:-secrets_deploy_pubkey}" "$(cut -d' ' -f1,2 $1.deploy.pub)" "$2" > $1.enroll
  }
  enrollment alpha alpha deploy_pubkey
  enrollment beta beta
  enrollment alpha2 alpha
  export SOPS_AGE_KEY_FILE=$PWD/maint.key
  # What fleet setup leaves: the maintainer's recovery recipient, cached.
  export XDG_STATE_HOME=$PWD/state
  mkdir -p state/fleet
  age-keygen -y maint.key > state/fleet/recovery.pub

  # One starting repository, as the real one starts, copied for each tool:
  # main holds a README and nothing else.
  mkdir start
  (
    cd start
    git init -q
    echo "# Secrets" > README.md
    git add -A
    git commit -q -m init
  )
  cp -r start sh
  cp -r start go
  # Each copy has an origin, as a clone does: the tools keep the commit they
  # trusted last on every branch, per repository, named after its address.
  for repo in sh go; do
    git init -q --bare origin-$repo.git
    git -C $repo remote add origin "file://$PWD/origin-$repo.git"
  done

  sh_tool() { (cd sh && ${shellTool}/bin/fleet-secrets "$@"); }
  go_tool() { ${fleet}/bin/fleet secrets "$@" --repo go --yes; }

  # Unpacks a branch of a repository into out/REPO, to read its files.
  unpack() {  # unpack REPO BRANCH
    rm -rf out/$1
    mkdir -p out/$1
    git -C $1 archive $2 | tar -x -C out/$1
  }

  # The recipients of a file on a branch must be the host's and the recovery
  # key's.
  expect_recipients() {  # expect_recipients REPO BRANCH FILE IDENTITY
    unpack $1 $2
    { age-keygen -y $4.key; age-keygen -y maint.key; } | sort > want.recipients
    yq '.sops.age[].recipient' out/$1/$3 | sort | diff want.recipients - \
      || { echo "$1 $2:$3: not encrypted to $4 and the recovery key"; exit 1; }
  }

  # The same, for a file of a listed user: the user's recipient too.
  expect_recipients_of() {  # expect_recipients_of REPO BRANCH FILE IDENTITY...
    local repo=$1 branch=$2 file=$3 id
    shift 3
    unpack $repo $branch
    { for id in "$@"; do age-keygen -y $id.key; done; age-keygen -y maint.key; } | sort > want.recipients
    yq '.sops.age[].recipient' out/$repo/$file | sort | diff want.recipients - \
      || { echo "$repo $branch:$file: not encrypted to $* and the recovery key"; exit 1; }
  }

  # The same as the other copy, but for what is random.
  compare() {  # compare WHAT-JUST-RAN
    echo "== after: $1"
    for repo in sh go; do
      [ "$(git -C $repo symbolic-ref --short HEAD)" = main ] || { echo "$repo: not on main"; exit 1; }
      [ "$(git -C $repo worktree list | wc -l)" = 1 ] || { echo "$repo: a worktree is left"; git -C $repo worktree list; exit 1; }
      [ -z "$(git -C $repo status --porcelain)" ] || { echo "$repo: main is not clean"; exit 1; }
    done
    diff <(git -C sh for-each-ref --format='%(refname)') <(git -C go for-each-ref --format='%(refname)')
    diff <(git -C sh log --format=%s main) <(git -C go log --format=%s main)
    for b in $(git -C sh for-each-ref --format='%(refname:short)' refs/heads/host refs/heads/user); do
      diff <(git -C sh ls-tree -r --name-only $b) <(git -C go ls-tree -r --name-only $b)
      diff <(git -C sh log --format=%s $b) <(git -C go log --format=%s $b)
      diff <(git -C sh show $b:enroll) <(git -C go show $b:enroll)
      for repo in sh go; do
        git -C $repo verify-commit $b 2>/dev/null || { echo "$repo $b: the tip is not signed"; exit 1; }
        if git -C $repo ls-tree -r --name-only $b | grep -qE '^(fleet|\.sops)\.yaml$'; then
          echo "$repo $b: holds a registry or rules"; exit 1
        fi
        unpack $repo $b
      done
      for f in $(git -C sh ls-tree -r --name-only $b | grep -E '^(hosts|users)/.*\.yaml$'); do
        for repo in sh go; do
          sops -d --output-type json out/$repo/$f > $repo.json
          jq -S 'del(.password, .ssh_key, .ssh_key_pub)' $repo.json > $repo.values
          yq '.sops.age[].recipient' out/$repo/$f | sort > $repo.recipients
          if jq -e 'has("password")' $repo.json >/dev/null; then
            user=''${f#users/}; user=''${user%%/*}
            hash=$(jq -r .password $repo.json)
            perl -e 'exit(crypt($ARGV[0], $ARGV[1]) eq $ARGV[1] ? 0 : 1)' "pw-$user" "$hash" \
              || { echo "$repo/$b:$f: the hash does not verify the password"; exit 1; }
          fi
          if jq -e 'has("ssh_key")' $repo.json >/dev/null; then
            jq -r .ssh_key $repo.json > $repo.key
            chmod 600 $repo.key
            [ "$(ssh-keygen -y -f $repo.key | cut -d' ' -f1,2)" = "$(jq -r .ssh_key_pub $repo.json | cut -d' ' -f1,2)" ] \
              || { echo "$repo/$b:$f: the key pair does not match"; exit 1; }
            jq -r .ssh_key_pub $repo.json | cut -d' ' -f3 > $repo.comment
          fi
        done
        diff sh.values go.values
        diff sh.recipients go.recipients
        if [ -f sh.comment ]; then diff sh.comment go.comment; rm sh.comment go.comment; fi
      done
    done
  }

  sh_tool host-enroll alpha --from ../alpha.enroll
  go_tool host-enroll alpha --from alpha.enroll
  compare "host-enroll alpha"
  for repo in sh go; do
    diff <(git -C $repo show host/alpha:enroll) alpha.enroll
    expect_recipients $repo host/alpha hosts/alpha.yaml alpha
  done

  printf 'pw-alice\npw-alice\n' | sh_tool user-password alice alpha
  printf 'pw-alice' | go_tool user-password alice alpha --password-stdin
  compare "user-password alice alpha"
  for repo in sh go; do expect_recipients $repo host/alpha users/alice/alpha.yaml alpha; done

  sh_tool user-key alice alpha
  go_tool user-key alice alpha
  compare "user-key alice alpha"

  sh_tool host-enroll beta --from ../beta.enroll
  go_tool host-enroll beta --from beta.enroll
  printf 'pw-bob\npw-bob\n' | sh_tool user-password bob beta
  printf 'pw-bob' | go_tool user-password bob beta --password-stdin
  compare "host-enroll beta, user-password bob beta"
  for repo in sh go; do expect_recipients $repo host/beta users/bob/beta.yaml beta; done

  sh_tool host-rekey alpha --from ../alpha2.enroll
  go_tool host-rekey alpha --from alpha2.enroll
  compare "host-rekey alpha"
  for repo in sh go; do
    unpack $repo host/alpha
    diff <(git -C $repo show host/alpha:enroll) alpha2.enroll
    for f in hosts/alpha.yaml users/alice/alpha.yaml; do
      expect_recipients $repo host/alpha $f alpha2
      SOPS_AGE_KEY_FILE=$PWD/alpha2.key sops -d out/$repo/$f >/dev/null \
        || { echo "$repo/$f: the new identity cannot decrypt it"; exit 1; }
      if SOPS_AGE_KEY_FILE=$PWD/alpha.key sops -d out/$repo/$f >/dev/null 2>&1; then
        echo "$repo/$f: the old identity can still decrypt it"; exit 1
      fi
    done
    # Beta's branch is not touched by it.
    expect_recipients $repo host/beta hosts/beta.yaml beta
  done

  sh_tool host-retire beta
  go_tool host-retire beta
  compare "host-retire beta"
  for repo in sh go; do
    if git -C $repo rev-parse --verify -q refs/heads/host/beta >/dev/null; then
      echo "$repo: beta's branch is still there"; exit 1
    fi
    git -C $repo rev-parse --verify -q refs/heads/host/alpha >/dev/null \
      || { echo "$repo: alpha's branch went with it"; exit 1; }
  done

  # A run that does not finish leaves the repository as it found it. With
  # commits that cannot be signed, each command below writes its files, then
  # fails at the commit, and must put everything back: a new host's branch,
  # a re-key of committed files, a new user's file, a key in an existing one.
  state() {
    git -C go for-each-ref
    git -C go worktree list
    git -C go status --porcelain --untracked-files=all
    (cd go && find . -path ./.git -prune -o -type f -print | sort | xargs sha256sum)
    (cd go && find . -path ./.git -prune -o -type d -print | sort)
  }
  state > before.state
  git -C go config user.signingKey "$PWD/no-such-key.pub"
  unfinished() {  # unfinished WHAT: the last command must have failed at its commit and put everything back
    grep -q "stopped at step [0-9]*, Commit" go.out || { echo "$1: did not stop at the commit"; cat go.out; exit 1; }
    grep -q "Put back what this run changed" go.out || { echo "$1: nothing was put back"; cat go.out; exit 1; }
    state | diff before.state - || { echo "$1: the repository is not as it was"; exit 1; }
  }
  if go_tool host-enroll beta --from beta.enroll > go.out 2>&1; then echo "committed unsigned"; exit 1; fi
  unfinished "host-enroll beta"
  if go_tool host-rekey alpha --from alpha.enroll > go.out 2>&1; then echo "committed unsigned"; exit 1; fi
  unfinished "host-rekey alpha"
  if printf 'pw-carol' | go_tool user-password carol alpha --password-stdin > go.out 2>&1; then echo "committed unsigned"; exit 1; fi
  unfinished "user-password carol alpha"
  if go_tool user-key alice alpha > go.out 2>&1; then echo "committed unsigned"; exit 1; fi
  unfinished "user-key alice alpha"
  git -C go config --unset user.signingKey

  # Neither builds on a branch tip that no trusted key signed, such as a
  # commit someone else pushed, nor runs while git runs the repository's own
  # hook.
  refused() {  # refused WHAT MESSAGE: both tools must refuse host-rekey with MESSAGE
    if sh_tool host-rekey alpha --from ../alpha.enroll > sh.err 2>&1; then echo "sh: $1"; exit 1; fi
    grep -q "$2" sh.err || { echo "sh: $1, without saying so"; cat sh.err; exit 1; }
    if go_tool host-rekey alpha --from alpha.enroll > go.err 2>&1; then echo "go: $1"; exit 1; fi
    grep -q "$2" go.err || { echo "go: $1, without saying so"; cat go.err; exit 1; }
  }
  for repo in sh go; do
    git -C $repo checkout -q host/alpha
    git -C $repo -c commit.gpgSign=false commit -q --allow-empty -m "pushed by someone else"
    git -C $repo checkout -q main
  done
  refused "built on an unsigned tip" "not signed by a key in gpg.ssh.allowedSignersFile"
  for repo in sh go; do
    git -C $repo checkout -q host/alpha
    git -C $repo commit -q --allow-empty -m "signed again"
    git -C $repo checkout -q main
    git -C $repo config core.hooksPath .githooks
  done
  refused "ran the repository's own hook" "core.hooksPath is .githooks"
  for repo in sh go; do git -C $repo config --unset core.hooksPath; done
  # Nor while git checks signatures against a file inside a checkout, as
  # fleet setup used to set it: a pushed commit could add the key it is
  # signed with. An untracked copy of the keys stands for one.
  for repo in sh go; do
    cp allowed_signers $repo/allowed_signers.here
    git -C $repo config gpg.ssh.allowedSignersFile "$PWD/$repo/allowed_signers.here"
  done
  refused "checked signatures against a file in a checkout" "which is inside a checkout"
  for repo in sh go; do
    git -C $repo config --unset gpg.ssh.allowedSignersFile
    rm $repo/allowed_signers.here
  done
  # Nor without the recovery recipient fleet setup caches.
  XDG_STATE_HOME=$PWD/no-state refused "ran without the recovery recipient" "run fleet setup"

  # Both refuse the same mistakes, and enrolling a host again with the
  # identity it holds changes nothing.
  for repo in sh go; do git -C $repo rev-parse host/alpha > $repo.tip; done
  if go_tool host-enroll alpha --from alpha.enroll 2>&1 | tee go.err; then echo "enrolled alpha twice"; exit 1; fi
  grep -q "already enrolled with another identity; use host-rekey" go.err
  if sh_tool host-enroll alpha --from ../alpha.enroll 2>&1 | tee sh.err; then echo "enrolled alpha twice"; exit 1; fi
  grep -q "already enrolled with another identity; use host-rekey" sh.err
  sh_tool host-enroll alpha --from ../alpha2.enroll
  go_tool host-enroll alpha --from alpha2.enroll
  for repo in sh go; do
    [ "$(git -C $repo rev-parse host/alpha)" = "$(cat $repo.tip)" ] \
      || { echo "$repo: enrolling alpha again with its identity changed its branch"; exit 1; }
  done
  if go_tool host-enroll gamma --from alpha.enroll 2>&1 | tee go.err; then echo "accepted another host's file"; exit 1; fi
  grep -q "enrollment file is for host 'alpha', not 'gamma'" go.err
  if go_tool host-retire gamma 2>&1 | tee go.err; then echo "retired a host that is not enrolled"; exit 1; fi
  grep -q "host 'gamma' is not enrolled" go.err
  compare "the refused commands"

  # A host branch only origin has, as in a fresh clone: the command makes the
  # local branch from it and commits there, and the branch, with the commit,
  # is still there afterwards to be pushed.
  for repo in sh go; do
    git -C $repo push -q origin 'refs/heads/*:refs/heads/*'
    git -C $repo fetch -q origin
    git -C $repo branch -q -D host/alpha
  done
  printf 'pw-alice\npw-alice\n' | sh_tool user-password alice alpha
  printf 'pw-alice' | go_tool user-password alice alpha --password-stdin
  for repo in sh go; do
    git -C $repo rev-parse -q --verify refs/heads/host/alpha > /dev/null \
      || { echo "$repo: the branch made from origin's, and its commit, are gone"; exit 1; }
    [ "$(git -C $repo rev-parse host/alpha~1)" = "$(git -C $repo rev-parse origin/host/alpha)" ] \
      || { echo "$repo: the commit is not on the branch made from origin's"; exit 1; }
  done
  compare "user-password alice alpha, on a branch only origin had"

  # A listed user. fleet secrets user-enroll (the port only) lists alice on
  # alpha and moves her file to user/alice; from then on both tools work on that
  # branch for her, and must leave it the same. A second user, dave, has no
  # file yet and gets an empty one. user-enroll and user-retire push, to the
  # origin each copy got above.
  for u in alice dave alice2; do
    age-keygen -o $u.key 2>/dev/null
    ssh-keygen -q -t ed25519 -N "" -f $u.sign
    printf 'version: 1\nuser: %s\nage_recipient: %s\nsigning_key: %s %s@laptop\n' \
      "$(echo $u | tr -d 2)" "$(age-keygen -y $u.key)" "$(cut -d' ' -f1,2 $u.sign.pub)" $u > $u.uenroll
  done
  enroll_both() {  # enroll_both ARGS...: user-enroll in both copies
    for repo in sh go; do ${fleet}/bin/fleet secrets user-enroll "$@" --repo $repo --yes; done
  }
  # Her file as it is on the host's branch now.
  for repo in sh go; do
    unpack $repo host/alpha
    sops -d --output-type json out/$repo/users/alice/alpha.yaml | jq -S 'del(.password)' > alice.before.$repo
    jq -e 'has("ssh_key")' alice.before.$repo >/dev/null
  done

  enroll_both alice --from alice.uenroll
  compare "user-enroll alice"
  for repo in sh go; do
    git -C $repo rev-parse --verify -q refs/heads/user/alice >/dev/null || { echo "$repo: no user/alice"; exit 1; }
    diff <(git -C $repo ls-tree -r --name-only user/alice) <(printf 'enroll\nusers/alice/alpha.yaml\n')
    diff <(git -C $repo show user/alice:enroll) alice.uenroll
    diff <(git -C $repo log --format=%s user/alice) <(echo "user-enroll: alice")
    if git -C $repo ls-tree -r --name-only host/alpha | grep -q '^users/'; then
      echo "$repo: a user file is left on host/alpha"; exit 1
    fi
    [ "$(git -C $repo log -1 --format=%s host/alpha)" = "user-enroll: alice on alpha" ] || { echo "$repo: host/alpha has no enroll commit"; exit 1; }
    # origin has what was pushed
    for b in user/alice host/alpha; do
      [ "$(git -C $repo rev-parse $b)" = "$(git -C origin-$repo.git rev-parse $b)" ] || { echo "$repo: $b was not pushed"; exit 1; }
    done
    # The host's file lists her, encrypted; her file keeps what it held.
    unpack $repo host/alpha
    [ "$(sops -d --extract '["users"]["alice"]["age_recipient"]' out/$repo/hosts/alpha.yaml)" = "$(age-keygen -y alice.key)" ] \
      || { echo "$repo: hosts/alpha.yaml does not list alice"; exit 1; }
    sops -d --extract '["users"]["alice"]["signing_key"]' out/$repo/hosts/alpha.yaml | grep -q '^ssh-ed25519 ' \
      || { echo "$repo: no signing key listed"; exit 1; }
    ! grep -q "$(age-keygen -y alice.key)" out/$repo/hosts/alpha.yaml || { echo "$repo: the list is not encrypted"; exit 1; }
    expect_recipients_of $repo host/alpha hosts/alpha.yaml alpha2
    unpack $repo user/alice
    sops -d --output-type json out/$repo/users/alice/alpha.yaml | jq -S 'del(.password)' | diff alice.before.$repo - \
      || { echo "$repo: her file changed in the move"; exit 1; }
    expect_recipients_of $repo user/alice users/alice/alpha.yaml alpha2 alice
    SOPS_AGE_KEY_FILE=$PWD/alice.key sops -d out/$repo/users/alice/alpha.yaml >/dev/null \
      || { echo "$repo: alice cannot decrypt her file"; exit 1; }
    if SOPS_AGE_KEY_FILE=$PWD/alice.key sops -d out/$repo/hosts/alpha.yaml >/dev/null 2>&1; then
      echo "$repo: alice can decrypt the host's file"; exit 1
    fi
  done
  # The host's branch only changes by the move.
  for repo in sh go; do git -C $repo rev-parse host/alpha > $repo.tip; done

  # Both tools now write to user/alice for her, and not to host/alpha.
  printf 'pw-alice\npw-alice\n' | sh_tool user-password alice alpha
  printf 'pw-alice' | go_tool user-password alice alpha --password-stdin
  compare "user-password alice alpha (listed)"
  # The tip of her branch may be signed by her own key, which the host's list
  # holds, but not by one nobody knows.
  ssh-keygen -q -t ed25519 -N "" -f stranger.sign
  signed_commit() {  # signed_commit KEYFILE MESSAGE: an empty commit on user/alice in both copies
    for repo in sh go; do
      git -C $repo checkout -q user/alice
      git -C $repo -c user.signingKey=$PWD/$1 commit -q --allow-empty -m "$2"
      git -C $repo checkout -q main
    done
  }
  signed_commit stranger.sign "signed by a stranger"
  if sh_tool user-key alice alpha > sh.err 2>&1; then echo "sh: built on a branch signed by a stranger"; exit 1; fi
  grep -q "not signed by a key in gpg.ssh.allowedSignersFile" sh.err
  if go_tool user-key alice alpha > go.err 2>&1; then echo "go: built on a branch signed by a stranger"; exit 1; fi
  grep -q "not signed by a key in gpg.ssh.allowedSignersFile" go.err
  for repo in sh go; do
    git -C $repo checkout -q user/alice
    git -C $repo reset -q --hard HEAD~1
    git -C $repo checkout -q main
  done
  signed_commit alice.sign "signed by alice"
  sh_tool user-key alice alpha
  go_tool user-key alice alpha
  compare "user-key alice alpha (listed, the tip signed by alice)"
  for repo in sh go; do
    diff <(git -C $repo log --format=%s user/alice) <(printf 'user-key: alice@alpha\nsigned by alice\nuser-password: alice@alpha\nuser-enroll: alice\n')
    [ "$(git -C $repo rev-parse host/alpha)" = "$(cat $repo.tip)" ] || { echo "$repo: a command for a listed user changed host/alpha"; exit 1; }
    expect_recipients_of $repo user/alice users/alice/alpha.yaml alpha2 alice
  done

  # A user with no file gets an empty one, for the host named, and a new file
  # for a listed user is encrypted to the user as well.
  enroll_both dave --from dave.uenroll --host alpha
  compare "user-enroll dave --host alpha"
  for repo in sh go; do
    diff <(git -C $repo ls-tree -r --name-only user/dave) <(printf 'enroll\nusers/dave/alpha.yaml\n')
    expect_recipients_of $repo user/dave users/dave/alpha.yaml alpha2 dave
  done
  printf 'pw-dave\npw-dave\n' | sh_tool user-password dave alpha
  printf 'pw-dave' | go_tool user-password dave alpha --password-stdin
  compare "user-password dave alpha (listed)"
  for repo in sh go; do expect_recipients_of $repo user/dave users/dave/alpha.yaml alpha2 dave; done
  for repo in sh go; do
    git -C $repo rev-parse user/alice host/alpha > $repo.tips
  done

  # The same identity again changes nothing; another identity is refused.
  enroll_both alice --from alice.uenroll
  if go_tool user-enroll alice --from alice2.uenroll 2>&1 | tee go.err; then echo "enrolled alice with another identity"; exit 1; fi
  grep -q "already enrolled with another identity" go.err
  for repo in sh go; do
    [ "$(git -C $repo rev-parse user/alice host/alpha)" = "$(cat $repo.tips)" ] || { echo "$repo: enrolling alice again changed a branch"; exit 1; }
  done
  compare "user-enroll alice again"
  # A user branch tip no trusted key signed is not built on.
  for repo in sh go; do
    git -C $repo checkout -q user/dave
    git -C $repo -c commit.gpgSign=false commit -q --allow-empty -m "pushed by someone else"
    git -C $repo checkout -q main
  done
  if sh_tool user-key dave alpha > sh.err 2>&1; then echo "sh: built on an unsigned user branch"; exit 1; fi
  grep -q "not signed by a key in gpg.ssh.allowedSignersFile" sh.err
  if go_tool user-key dave alpha > go.err 2>&1; then echo "go: built on an unsigned user branch"; exit 1; fi
  grep -q "not signed by a key in gpg.ssh.allowedSignersFile" go.err
  for repo in sh go; do
    git -C $repo checkout -q user/dave
    git -C $repo reset -q --hard HEAD~1
    git -C $repo checkout -q main
  done
  compare "the refused commands on user/dave"

  # Re-keying the host re-keys its listed users' files on their branches too:
  # the host must still decrypt them, and its old identity must not.
  age-keygen -o alpha3.key 2>/dev/null
  ssh-keygen -q -t ed25519 -N "" -f alpha3.deploy
  enrollment alpha3 alpha
  sh_tool host-rekey alpha --from ../alpha3.enroll
  go_tool host-rekey alpha --from alpha3.enroll
  compare "host-rekey alpha (users listed)"
  for repo in sh go; do
    for u in alice dave; do
      expect_recipients_of $repo user/$u users/$u/alpha.yaml alpha3 $u
      [ "$(git -C $repo log -1 --format=%s user/$u)" = "host-rekey: alpha" ] || { echo "$repo: no host-rekey commit on user/$u"; exit 1; }
      SOPS_AGE_KEY_FILE=$PWD/alpha3.key sops -d out/$repo/users/$u/alpha.yaml >/dev/null \
        || { echo "$repo: the new identity cannot decrypt $u's file"; exit 1; }
      if SOPS_AGE_KEY_FILE=$PWD/alpha2.key sops -d out/$repo/users/$u/alpha.yaml >/dev/null 2>&1; then
        echo "$repo: the old identity can still decrypt $u's file"; exit 1
      fi
      SOPS_AGE_KEY_FILE=$PWD/$u.key sops -d out/$repo/users/$u/alpha.yaml >/dev/null \
        || { echo "$repo: $u cannot decrypt their file"; exit 1; }
    done
    expect_recipients_of $repo host/alpha hosts/alpha.yaml alpha3
  done

  # user-retire takes dave off the host and deletes his local branch.
  for repo in sh go; do ${fleet}/bin/fleet secrets user-retire dave --repo $repo --yes | tee $repo.out; done
  grep -q "push origin --delete user/dave" go.out
  compare "user-retire dave"
  for repo in sh go; do
    if git -C $repo rev-parse --verify -q refs/heads/user/dave >/dev/null; then echo "$repo: user/dave is still there"; exit 1; fi
    unpack $repo host/alpha
    [ "$(sops -d --extract '["users"]["alice"]["age_recipient"]' out/$repo/hosts/alpha.yaml)" = "$(age-keygen -y alice.key)" ] \
      || { echo "$repo: retiring dave unlisted alice"; exit 1; }
    if sops -d --extract '["users"]["dave"]' out/$repo/hosts/alpha.yaml >/dev/null 2>&1; then echo "$repo: dave is still listed"; exit 1; fi
    [ "$(git -C $repo log -1 --format=%s host/alpha)" = "user-retire: dave on alpha" ] || { echo "$repo: no retire commit"; exit 1; }
    [ "$(git -C $repo rev-parse host/alpha)" = "$(git -C origin-$repo.git rev-parse host/alpha)" ] || { echo "$repo: the retire commit was not pushed"; exit 1; }
  done

  # fleet secrets user-keys (the port only) writes alice's login and sudo keys
  # on her branch too.
  ssh-keygen -q -t ed25519 -N "" -f alice.login
  cp alice.login.pub alice.keys
  go_tool user-keys alice alpha --authorized-keys alice.keys --sudo-keys alice.keys
  unpack go user/alice
  [ "$(git -C go log -1 --format=%s user/alice)" = "user-keys: alice on alpha" ] || { echo "go: no user-keys commit on user/alice"; exit 1; }
  [ "$(sops -d --extract '["authorized_keys"]' out/go/users/alice/alpha.yaml)" = "$(cat alice.keys | tr -d '\n')" ] \
    || { echo "go: authorized_keys on user/alice is not what was given"; exit 1; }
  [ "$(git -C go rev-parse host/alpha)" = "$(git -C origin-go.git rev-parse host/alpha)" ]
  [ "$(git -C go rev-parse user/alice)" = "$(git -C origin-go.git rev-parse user/alice)" ] || { echo "go: user-keys did not push user/alice"; exit 1; }
  expect_recipients_of go user/alice users/alice/alpha.yaml alpha3 alice

  # The commit each tool trusted last on a branch (fleet/repos/<id>/trusted/
  # <branch> in the state folder): both write the same files, and the id comes
  # from the origin's address however it is written. host-rekey with the
  # identity the host holds already stops after the checks of its branch's
  # tip, so it checks and records and changes nothing.
  record_of() {  # record_of REPO BRANCH: the file under the origin's id
    echo "state/fleet/repos/file$PWD/origin-$1/trusted/$2"
  }
  check_alpha() {  # check_alpha TOOL: the checks of host/alpha's tip, which stops before any change
    if [ "$1" = sh ]; then sh_tool host-rekey alpha --from ../alpha3.enroll > $1.err 2>&1 || true
    else go_tool host-rekey alpha --from alpha3.enroll > $1.err 2>&1 || true; fi
  }
  for tool in sh go; do
    check_alpha $tool
    [ "$(cat "$(record_of $tool host/alpha)")" = "$(git -C $tool rev-parse host/alpha)" ] \
      || { echo "$tool: the record of host/alpha is not its tip"; cat $tool.err; exit 1; }
    [ "$(stat -c %a "$(record_of $tool host/alpha)")" = 600 ] || { echo "$tool: the record is not private"; exit 1; }
  done
  # A listed user's branch is recorded under its own name.
  for repo in sh go; do
    [ -f "$(record_of $repo user/alice)" ] || { echo "$repo: no record of user/alice"; exit 1; }
    [ ! -e "$(record_of $repo host/beta)" ] || { echo "$repo: a retired host's record is left"; exit 1; }
    [ ! -e "$(record_of $repo user/dave)" ] || { echo "$repo: a retired user's record is left"; exit 1; }
  done
  # The same address in every written form gives the same id for both.
  for form in git@example.test:o/r.git ssh://git@example.test/o/r.git https://example.test/o/r https://EXAMPLE.test/o/r.git/; do
    for tool in sh go; do
      git -C $tool remote set-url origin "$form"
      rm -rf state/fleet/repos/example.test
      check_alpha $tool
      [ "$(cat state/fleet/repos/example.test/o/r/trusted/host/alpha)" = "$(git -C $tool rev-parse host/alpha)" ] \
        || { echo "$tool: $form is not recorded as example.test/o/r"; cat $tool.err; exit 1; }
    done
  done
  rm -rf state/fleet/repos/example.test
  for repo in sh go; do git -C $repo remote set-url origin "file://$PWD/origin-$repo.git"; done

  # A branch whose tip was reset to an older signed commit, as a pushed old
  # main would be served again, is refused by both with the record named;
  # with the record removed both take it.
  for tool in sh go; do
    git -C $tool rev-parse host/alpha > $tool.newest
    git -C $tool branch -q -f host/alpha host/alpha~1
    git -C $tool verify-commit host/alpha 2>/dev/null
    check_alpha $tool
    grep -q "host/alpha at $(git -C $tool rev-parse host/alpha) does not follow $(cat $tool.newest), the commit you trusted last" $tool.err \
      || { echo "$tool: took an older signed tip, or without naming the record"; cat $tool.err; exit 1; }
    grep -q "rm $PWD/$(record_of $tool host/alpha)" $tool.err || { echo "$tool: did not say which record to remove"; cat $tool.err; exit 1; }
    [ "$(cat "$(record_of $tool host/alpha)")" = "$(cat $tool.newest)" ] || { echo "$tool: the refusal moved the record"; exit 1; }
    rm "$(record_of $tool host/alpha)"
    check_alpha $tool
    if grep -q "does not follow" $tool.err; then echo "$tool: refused after the record was removed"; cat $tool.err; exit 1; fi
    [ "$(cat "$(record_of $tool host/alpha)")" = "$(git -C $tool rev-parse host/alpha)" ] \
      || { echo "$tool: the older tip was not recorded afresh"; exit 1; }
  done

  echo "parity: the shell tool and the port agree on every command"
  touch $out
''
