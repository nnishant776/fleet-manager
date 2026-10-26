# Runs the shell fleet-secrets (lib/apps/fleet-secrets.nix) and its Go port
# (`fleet secrets …`) through the same commands, on two copies of one scratch
# repository, and checks after every command that they left it the same:
# - the same branches (one host/<name> per host) and none left over, the same
#   tracked files on each and the same commit messages, every tip signed, and
#   the checkout still on main with no worktree left behind;
# - every encrypted file decrypting to the same values, for the same
#   recipients, which are the host's and the recovery key's and no others.
# Password hashes and SSH keys are random, so those are checked for what they
# must be instead: the hash verifies the password, and the key pair matches,
# with the same comment. After the re-key and the retirement it also checks
# who can still decrypt what.
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
    for b in $(git -C sh for-each-ref --format='%(refname:short)' refs/heads/host); do
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
    git clone -q --bare $repo $repo-origin.git
    git -C $repo remote add origin "$PWD/$repo-origin.git"
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

  echo "parity: the shell tool and the port agree on every command"
  touch $out
''
