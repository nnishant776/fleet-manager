# Runs `fleet inventory import` on a scratch fleet-manager and a scratch
# inventory with real git and signing. A stub `nix` answers what the command
# asks of the flakes: the layout, the accounts and addresses, and what the
# imported host evaluates to, which it reads from the files the import copied.
# The checks:
# - the host's folder in the inventory has the signers path one level deeper
#   and the inventory's address, and its other files as they were;
# - the commit is on origin's main, signed by the trusted key, and the host's
#   branch holds flake.nix, flake.lock, fleet/ and the host's folder only;
# - the output lists the commands that finish the move, and the files in
#   fleet-manager that still name the host;
# - the host cannot be imported twice, and a host that reads a file outside
#   its folder is refused with nothing written.
# Run it with `nix build .#checks.x86_64-linux.inventory-import`.
{ pkgs, fleet }:
pkgs.runCommand "inventory-import" {
  nativeBuildInputs = with pkgs; [ git openssh gnutar coreutils diffutils gnugrep gnused ];
} ''
  set -euo pipefail
  export HOME=$PWD/home XDG_STATE_HOME=$PWD/state XDG_DATA_HOME=$PWD/data
  export GIT_TERMINAL_PROMPT=0
  mkdir -p "$HOME" bin
  git config --global user.name test
  git config --global user.email test@example.com
  git config --global init.defaultBranch main
  ssh-keygen -q -t ed25519 -N "" -f signing
  echo "test namespaces=\"git\" $(cut -d' ' -f1,2 signing.pub)" > signers
  git config --global gpg.format ssh
  git config --global user.signingKey "$PWD/signing"
  git config --global commit.gpgSign true

  export INV=$PWD/inv
  cat > bin/nix <<'EOF2'
  #!/bin/sh
  case "$*" in
    *"#fleetLayout"*) echo '{"hostBranches":true,"hosts":"config/hosts","users":"config/users","shared":[]}' ;;
    *"git+file://"*"nixosConfigurations.bare.config.conf.secrets.repo.url"*) echo null ;;
    *"git+file://"*"conf.secrets.repo.url"*) echo '"ssh://git@forge/secrets.git"' ;;
    *"c.config.conf.secrets.repo.url) cs"*) echo '{"alpha":"ssh://git@forge/secrets.git"}' ;;
    *"git+file://"*"conf.users.accounts"*) echo '[]' ;;
    *"c.config.conf.fleet.repo.url) cs"*) echo '{"alpha":"ssh://git@forge/inventory.git"}' ;;
    *"attrNames c.config.conf.users.accounts"*)
      case "$*" in *darwinConfigurations*) echo '{}' ;; *) echo '{"alpha":[],"mini":[]}' ;; esac ;;
    *darwinConfigurations*) echo '{}' ;;
    *"toplevel.drvPath) cs"*) echo '{"alpha":"/nix/store/alpha.drv","mini":"/nix/store/mini.drv"}' ;;
    *"path:"*"nixosConfigurations.mini.config.system.build.toplevel.drvPath"*) echo /nix/store/mini.drv ;;
    *"nixosConfigurations.mini.config.conf.fleet.repo.url"*)
      grep conf.fleet.repo.url "$INV/config/hosts/mini/default.nix" | sed 's/.*= \(".*"\);.*/\1/' ;;
    *"nixosConfigurations.mini.config.conf.fleet.repo.ref"*) echo '"host/mini"' ;;
    *"nixosConfigurations.mini.config.conf.fleet.signing.allowedSignersFile"*) cat "$INV/fleet/allowed_signers" ;;
    *"nixosConfigurations.mini.config.conf.secrets.repo.url"*) echo '""' ;;
    *) echo "stub nix: not expected: $*" >&2; exit 1 ;;
  esac
  EOF2
  chmod +x bin/nix
  export PATH=$PWD/bin:$PATH

  # fleet-manager: a bare origin, a work clone, signed commits. Its origin
  # is then its public address, which this machine cannot fetch from; the
  # clone holds main as fetched.
  git init -q --bare fm-origin.git
  git clone -q "file://$PWD/fm-origin.git" fm 2>/dev/null
  (
    cd fm
    mkdir -p fleet hosts/mini hosts/other shared
    cp ../signers fleet/allowed_signers
    cat > hosts/mini/default.nix <<'EOF3'
  { ... }: {
    imports = [ ./hardware.nix ];
    nixpkgs.hostPlatform = "x86_64-linux";
    conf.fleet.repo.url = "https://github.com/nnishant776/fleet-manager";
    conf.fleet.signing.allowedSignersFile = ../../fleet/allowed_signers;
  }
  EOF3
    echo '{ ... }: { fileSystems."/".device = "/dev/disk/by-label/nixos"; }' > hosts/mini/hardware.nix
    echo '{ ... }: { } # mini is the host that moves' > hosts/other/default.nix
    echo '{ ... }: { }' > shared/x.nix
    git add -A
    git commit -q -m "fleet-manager"
    git push -q origin main
    git remote set-url origin https://github.com/nnishant776/fleet-manager
    git update-ref refs/remotes/origin/main HEAD
  )

  git init -q --bare origin.git
  git clone -q "file://$PWD/origin.git" inv 2>/dev/null
  (
    cd inv
    mkdir -p fleet config/hosts/alpha
    cp ../signers fleet/allowed_signers
    echo '{ }' > flake.nix
    echo '{ }' > flake.lock
    echo alpha-1 > config/hosts/alpha/default.nix
    git add -A
    git commit -q -m inventory
    git push -q origin main
  )
  for c in fm inv; do
    ${fleet}/bin/fleet trust signers --flake $c --yes > trust.out 2>&1 || { cat trust.out; exit 1; }
  done

  imp() { ${fleet}/bin/fleet inventory import "$@" --from fm --flake inv --yes; }
  files() { git -C origin.git ls-tree -r --name-only "$1"; }

  imp mini > out 2>&1 || { cat out; exit 1; }
  d=inv/config/hosts/mini/default.nix
  grep -qF '../../../fleet/allowed_signers' $d || { echo "the signers path is not one level deeper"; cat $d; exit 1; }
  grep -qF '"ssh://git@forge/inventory.git"' $d || { echo "the address is not the inventory's"; cat $d; exit 1; }
  if grep -q 'github.com' $d; then echo "the copy names fleet-manager"; cat $d; exit 1; fi
  cmp fm/hosts/mini/hardware.nix inv/config/hosts/mini/hardware.nix
  [ "$(git -C origin.git log -1 --format=%s main)" = "feat(hosts): mini moves here from fleet-manager" ] \
    || { echo "main lacks the commit"; cat out; exit 1; }
  git -C origin.git -c gpg.ssh.allowedSignersFile="$PWD/signers" verify-commit main 2>/dev/null \
    || { echo "main is not signed by the trusted key"; exit 1; }
  printf '%s\n' config/hosts/mini/default.nix config/hosts/mini/hardware.nix flake.lock flake.nix fleet/allowed_signers \
    | diff - <(files host/mini) || { echo "host/mini holds the wrong files"; exit 1; }
  git -C origin.git -c gpg.ssh.allowedSignersFile="$PWD/signers" verify-commit host/mini 2>/dev/null \
    || { echo "host/mini is not signed by the trusted key"; exit 1; }
  [ -z "$(git -C origin.git ls-tree -r --name-only main | grep alpha | grep -v config/hosts/alpha || true)" ]
  if git -C origin.git rev-parse -q --verify host/alpha >/dev/null; then echo "published a branch for another host"; exit 1; fi
  for want in "fleet host enroll mini" "fleet host check mini" "git rm -r hosts/mini" "hosts/other/default.nix"; do
    grep -qF "$want" out || { echo "the output lacks: $want"; cat out; exit 1; }
  done
  [ -z "$(git -C inv status --porcelain)" ] || { echo "the inventory checkout is not clean"; exit 1; }

  # Again: the host is there.
  if imp mini > out 2>&1; then echo "imported a host twice"; exit 1; fi
  grep -q "mini is in the inventory already" out || { cat out; exit 1; }

  # A host that reads a file outside its folder is refused, nothing written.
  (
    cd fm
    mkdir -p hosts/bad
    echo '{ ... }: { imports = [ ../../shared/x.nix ]; }' > hosts/bad/default.nix
    git add -A
    git commit -q -m "a host that reads outside its folder"
  )
  if imp bad > out 2>&1; then echo "imported a host that reads outside its folder"; exit 1; fi
  grep -qF "reads ../../shared/x.nix, outside its folder" out || { cat out; exit 1; }
  [ ! -e inv/config/hosts/bad ] || { echo "wrote the refused host"; exit 1; }
  [ -z "$(git -C inv status --porcelain)" ] || { echo "the refused import left the checkout dirty"; exit 1; }

  echo "import: a host moves with its signers path and address adjusted, and its branch is published"
  touch $out
''
