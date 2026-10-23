# Runs `fleet inventory publish` on a scratch inventory with real git and
# signing. A stub `nix` answers what publish asks of the flake: its layout
# and each host's accounts. The checks:
# - each host's branch holds flake.nix, flake.lock, fleet/, its own folder,
#   its users' folders and the shared paths, and nothing of the other host;
# - the branches have histories of their own, signed by a trusted key;
# - publishing again changes nothing, and a change to one host moves only
#   its branch, on top of its last commit;
# - a clone of one branch alone never sees the other host's files;
# - a commit someone else pushed to a branch, unsigned, is not built on, and
#   an unsigned main is not published.
# Run it with `nix build .#checks.x86_64-linux.inventory-publish`.
{ pkgs, fleet }:
pkgs.runCommand "inventory-publish" {
  nativeBuildInputs = with pkgs; [ git openssh gnutar coreutils diffutils ];
} ''
  set -euo pipefail
  export HOME=$PWD/home XDG_STATE_HOME=$PWD/state
  mkdir -p "$HOME" bin
  git config --global user.name test
  git config --global user.email test@example.com
  git config --global init.defaultBranch main
  ssh-keygen -q -t ed25519 -N "" -f signing
  echo "test namespaces=\"git\" $(cut -d' ' -f1,2 signing.pub)" > signers
  git config --global gpg.format ssh
  git config --global user.signingKey "$PWD/signing"
  git config --global commit.gpgSign true

  cat > bin/nix <<'EOF'
  #!/bin/sh
  case "$*" in
    *"#fleetLayout"*) echo '{"hostBranches":true,"hosts":"config/hosts","users":"config/users","shared":["config/common"]}' ;;
    *"#nixosConfigurations --apply"*) echo '{"alpha":["alice","admin"],"beta":["bob"]}' ;;
    *"#darwinConfigurations --apply"*) echo '{}' ;;
    *) echo "stub nix: not expected: $*" >&2; exit 1 ;;
  esac
  EOF
  chmod +x bin/nix
  export PATH=$PWD/bin:$PATH

  git init -q --bare origin.git
  git clone -q "file://$PWD/origin.git" inv 2>/dev/null
  (
    cd inv
    mkdir -p fleet config/hosts/alpha config/hosts/beta config/users/alice config/users/bob config/common
    cp ../signers fleet/allowed_signers
    echo '{ }' > flake.nix
    echo '{ }' > flake.lock
    echo alpha-1 > config/hosts/alpha/default.nix
    echo beta-1 > config/hosts/beta/default.nix
    echo alice > config/users/alice/default.nix
    echo bob > config/users/bob/default.nix
    echo common > config/common/default.nix
    git add -A
    git commit -q -m inventory
    git push -q origin main
  )
  ${fleet}/bin/fleet trust signers --flake inv --yes > trust.out 2>&1 || { cat trust.out; exit 1; }

  publish() { ${fleet}/bin/fleet inventory publish --flake inv --yes --no-check "$@"; }
  files() { git -C origin.git ls-tree -r --name-only "$1"; }
  rev() { git -C origin.git rev-parse "$1"; }

  publish > out 2>&1 || { cat out; exit 1; }
  printf '%s\n' config/common/default.nix config/hosts/alpha/default.nix config/users/alice/default.nix \
    flake.lock flake.nix fleet/allowed_signers | diff - <(files host/alpha) \
    || { echo "host/alpha holds the wrong files"; exit 1; }
  printf '%s\n' config/common/default.nix config/hosts/beta/default.nix config/users/bob/default.nix \
    flake.lock flake.nix fleet/allowed_signers | diff - <(files host/beta) \
    || { echo "host/beta holds the wrong files"; exit 1; }
  for b in host/alpha host/beta; do
    git -C origin.git -c gpg.ssh.allowedSignersFile="$PWD/signers" verify-commit "$b" 2>/dev/null \
      || { echo "$b is not signed by the trusted key"; exit 1; }
    [ "$(git -C origin.git rev-list --count "$b")" = 1 ] || { echo "$b has main's history"; exit 1; }
  done
  git -C origin.git log -1 --format=%B host/alpha | grep -q "Cut from main at $(rev main)"

  a1=$(rev host/alpha) b1=$(rev host/beta)
  publish > out 2>&1 || { cat out; exit 1; }
  grep -q "every branch is up to date" out || { echo "published unchanged branches"; cat out; exit 1; }
  [ "$(rev host/alpha)" = "$a1" ] && [ "$(rev host/beta)" = "$b1" ]

  (cd inv && echo beta-2 > config/hosts/beta/default.nix && git commit -qam beta && git push -q origin main)
  publish > out 2>&1 || { cat out; exit 1; }
  [ "$(rev host/alpha)" = "$a1" ] || { echo "alpha moved with beta's change"; exit 1; }
  [ "$(rev host/beta^)" = "$b1" ] || { echo "host/beta's new commit is not on its last"; exit 1; }

  # One branch alone: none of the other host's files.
  git clone -q --single-branch --branch host/alpha "file://$PWD/origin.git" alpha-only 2>/dev/null
  for f in config/users/bob/default.nix config/hosts/beta/default.nix; do
    blob=$(git -C origin.git rev-parse "main:$f")
    if git -C alpha-only cat-file -e "$blob" 2>/dev/null; then echo "a clone of host/alpha has $f"; exit 1; fi
  done

  # Someone else pushes an unsigned commit to host/alpha: it is not built on.
  git clone -q --single-branch --branch host/alpha "file://$PWD/origin.git" other 2>/dev/null
  (cd other && echo x > extra && git add extra && git -c commit.gpgSign=false commit -qm extra && git push -q origin host/alpha)
  (cd inv && echo alpha-2 > config/hosts/alpha/default.nix && git commit -qam alpha && git push -q origin main)
  if publish --host alpha > out 2>&1; then echo "published on an unsigned branch commit"; exit 1; fi
  grep -q "is not signed by a key you trust" out || { cat out; exit 1; }

  # Nor is an unsigned main published.
  (cd inv && echo beta-3 > config/hosts/beta/default.nix && git -c commit.gpgSign=false commit -qam unsigned && git push -q origin main)
  if publish --host beta > out 2>&1; then echo "published from an unsigned main"; exit 1; fi
  grep -q "is not signed by a key you trust" out || { cat out; exit 1; }

  echo "publish: each host's branch holds its own files only"
  touch $out
''
