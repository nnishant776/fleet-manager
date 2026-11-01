# Runs `fleetmgr host new` on a scratch inventory and a scratch secrets
# repository with real git and signing. A stub `nix` answers what the command
# asks of the flakes: the layout, the hosts' addresses, timezone, locale and
# release, and what the new host evaluates to, which it reads from the file
# the command wrote. The inventory is fetched over a stand-in for ssh that runs
# the git command on this machine, so that its address is an SSH one, as a
# private inventory's is; fleet-manager's address is rewritten to a local
# repository the same way. The checks:
# - part 1, with every answer a flag: config/hosts/desk1/default.nix is written
#   and committed with the subject the command knows its own by, signed,
#   pushed to origin's main, and host/desk1 is published; install prep's
#   checks pass and print the install line;
# - part 2 stops with no enrollment file, saying to give --from;
# - with an enrollment file made by `fleetmgr-secrets-sync init`, host/desk1
#   of the secrets repository holds hosts/desk1.yaml encrypted to desk1 and to
#   the recovery recipient, and support's keys; it is pushed, signed, and both
#   deploy keys went to the register-with command;
# - the same command again, support's keys and all, changes no branch in any
#   repository, and nor does one without --support-keys.
# The install itself is not run: it fetches and builds what the installer
# installs, which a sandbox cannot.
# Run it with `nix build .#checks.x86_64-linux.fleet-host-new`.
{ pkgs, fleetmgr, secretsSync }:
pkgs.runCommand "fleet-host-new" {
  nativeBuildInputs = with pkgs; [ git openssh gnutar coreutils diffutils gnugrep gnused age sops mkpasswd yq-go jq ];
} ''
  set -euo pipefail
  export HOME=$PWD/home XDG_STATE_HOME=$PWD/state XDG_DATA_HOME=$PWD/data
  export GIT_TERMINAL_PROMPT=0
  mkdir -p "$HOME" bin state/fleet
  git config --global user.name test
  git config --global user.email test@example.com
  git config --global init.defaultBranch main
  ssh-keygen -q -t ed25519 -N "" -f signing
  echo "test namespaces=\"git\" $(cut -d' ' -f1,2 signing.pub)" > signers
  git config --global gpg.format ssh
  git config --global user.signingKey "$PWD/signing"
  git config --global commit.gpgSign true
  # The secrets repository's commits are checked against this, outside every
  # checkout, as fleetmgr setup arranges.
  git config --global gpg.ssh.allowedSignersFile "$PWD/signers"

  # The maintainer's recovery identity, and its recipient as fleetmgr setup caches it.
  age-keygen -o maint.key 2>/dev/null
  age-keygen -y maint.key > state/fleet/recovery.pub
  export SOPS_AGE_KEY_FILE=$PWD/maint.key

  # ssh, as git calls it, runs the command it is given on this machine.
  cat > bin/fakessh <<'EOF2'
  #!/bin/sh
  for a; do cmd=$a; done
  exec sh -c "$cmd"
  EOF2
  chmod +x bin/fakessh
  export GIT_SSH_COMMAND=$PWD/bin/fakessh

  export INV=$PWD/inv
  export INV_URL="ssh://git@forge$PWD/origin.git"
  cat > bin/nix <<'EOF2'
  #!/bin/sh
  case "$*" in
    *"#fleetLayout"*) echo '{"hostBranches":true,"hosts":"config/hosts","users":null,"shared":[]}' ;;
    *"c.config.conf.fleet.repo.url) cs"*) printf '{"alpha":"%s"}\n' "$INV_URL" ;;
    *"c.config.conf.secrets.repo.url) cs"*) echo '{"alpha":"ssh://git@forge/secrets.git"}' ;;
    *"c.config.conf.host.timezone) cs"*) echo '{"alpha":"Europe/Berlin"}' ;;
    *"c.config.conf.host.locale) cs"*) echo '{"alpha":"en_US.UTF-8"}' ;;
    *"system.nixos.release"*) echo 26.05 ;;
    *"attrNames c.config.conf.users.accounts"*)
      case "$*" in *darwinConfigurations*) echo '{}' ;; *) echo '{"alpha":[],"desk1":["alice"]}' ;; esac ;;
    *darwinConfigurations*) echo '{}' ;;
    *"toplevel.drvPath) cs"*) echo '{"alpha":"/nix/store/alpha.drv","desk1":"/nix/store/desk1.drv"}' ;;
    *"path:"*"nixosConfigurations.desk1.config.system.build.toplevel.drvPath"*) echo /nix/store/desk1.drv ;;
    "eval --raw "*"nixosConfigurations.desk1.config.conf.fleet.repo.url"*) printf '%s' "$INV_URL" ;;
    "eval --raw "*"nixosConfigurations.desk1.config.conf.fleet.repo.ref"*) printf 'host/desk1' ;;
    *"nixosConfigurations.desk1.config.conf.fleet.repo.url"*) printf '"%s"\n' "$INV_URL" ;;
    *"nixosConfigurations.desk1.config.conf.fleet.repo.ref"*) echo '"host/desk1"' ;;
    *"nixosConfigurations.desk1.config.conf.fleet.signing.allowedSignersFile"*) cat "$INV/fleet/allowed_signers" ;;
    *"nixosConfigurations.desk1.config.conf.secrets.repo.url"*) echo '"ssh://git@forge/secrets.git"' ;;
    *"nixosConfigurations.desk1.config.conf.users.accounts"*) echo '["alice"]' ;;
    *) echo "stub nix: not expected: $*" >&2; exit 1 ;;
  esac
  EOF2
  chmod +x bin/nix
  export PATH=$PWD/bin:${fleetmgr}/bin:${secretsSync}/bin:$PATH

  # fleet-manager: a bare origin with a signed bootstrap that supports pinned
  # installs. Its public address is rewritten to it once the keys are trusted.
  git init -q --bare fm-origin.git
  git clone -q "file://$PWD/fm-origin.git" fm 2>/dev/null
  (
    cd fm
    mkdir -p fleet
    cp ../signers fleet/allowed_signers
    printf '#!/bin/sh\n# pinned-installs: 1 test\n' > bootstrap
    git add -A
    git commit -q -m "fleet-manager"
    git push -q origin main
    git remote set-url origin https://github.com/nnishant776/fleet-manager
    git update-ref refs/remotes/origin/main HEAD
  )
  fm_rev=$(git -C fm rev-parse HEAD)
  fleetmgr trust signers --flake fm --yes > trust.out 2>&1 || { cat trust.out; exit 1; }
  git config --global url."file://$PWD/fm-origin.git".insteadOf https://github.com/nnishant776/fleet-manager
  # The fleet-manager commit you trusted last, as fleetmgr repo add records it.
  mkdir -p state/fleet/repos/github.com/nnishant776/fleet-manager/trusted
  echo "$fm_rev" > state/fleet/repos/github.com/nnishant776/fleet-manager/trusted/main
  chmod 600 state/fleet/repos/github.com/nnishant776/fleet-manager/trusted/main

  # The inventory: one host, alpha, pinning that fleet-manager commit.
  git init -q --bare origin.git
  git clone -q "file://$PWD/origin.git" inv 2>/dev/null
  (
    cd inv
    mkdir -p fleet config/hosts/alpha
    cp ../signers fleet/allowed_signers
    echo '{ }' > flake.nix
    printf '{"nodes":{"fleet-manager":{"locked":{"type":"git","url":"https://github.com/nnishant776/fleet-manager","rev":"%s"}}},"root":"root","version":7}\n' "$fm_rev" > flake.lock
    echo alpha-1 > config/hosts/alpha/default.nix
    git add -A
    git commit -q -m inventory
    git push -q origin main
    git remote set-url origin "$INV_URL"
    git fetch -q origin main
  )
  fleetmgr trust signers --flake inv --yes > trust.out 2>&1 || { cat trust.out; exit 1; }

  # The secrets repository, as fleetmgr-secrets starts it.
  git init -q --bare secrets-origin.git
  git clone -q "file://$PWD/secrets-origin.git" secrets 2>/dev/null
  (
    cd secrets
    echo "# Secrets" > README.md
    git add -A
    git commit -q -m "fleetmgr-secrets, empty"
    git push -q origin main
  )

  ssh-keygen -q -t ed25519 -N "" -C support@maintainer -f support-key
  cp support-key.pub support-keys.pub

  new() {
    fleetmgr host new desk1 --flake inv --repo secrets --yes \
      --platform x86_64-linux --role developer --machine-type desktop --boot uefi --disk /dev/nvme0n1 \
      --network networkmanager --account 'alice:Alice Liddell:admin' --state-version 26.05 \
      --no-password alice --register-with 'cat >> registered' "$@"
  }
  refs() { git -C "$1" for-each-ref --format='%(refname) %(objectname)'; }

  # Part 1, and part 2 stopping for want of the enrollment file.
  if new > out1 2>&1; then echo "part 2 went on with no enrollment file"; cat out1; exit 1; fi
  grep -qF -e "--from" out1 || { echo "the stop does not say to give --from"; cat out1; exit 1; }
  d=inv/config/hosts/desk1/default.nix
  [ -f $d ] || { echo "default.nix was not written"; cat out1; exit 1; }
  [ ! -e inv/config/hosts/desk1/hardware-configuration.nix ] || { echo "wrote a hardware configuration"; exit 1; }
  for want in 'nixpkgs.hostPlatform = "x86_64-linux";' 'system.stateVersion = "26.05";' 'locale = "en_US.UTF-8";' \
    'device = "/dev/nvme0n1";' 'encrypt = true;' "users.accounts.alice" "privileged = true;" "$INV_URL"; do
    grep -qF -e "$want" $d || { echo "default.nix lacks: $want"; cat $d; exit 1; }
  done
  [ "$(git -C origin.git log -1 --format=%s main)" = "feat(hosts): desk1, set up by fleetmgr host new" ] \
    || { echo "main lacks the commit"; cat out1; exit 1; }
  git -C origin.git -c gpg.ssh.allowedSignersFile="$PWD/signers" verify-commit main 2>/dev/null \
    || { echo "main is not signed by the trusted key"; exit 1; }
  [ "$(git -C origin.git show main:config/hosts/desk1/default.nix)" = "$(cat $d)" ]
  git -C origin.git rev-parse -q --verify host/desk1 > /dev/null || { echo "host/desk1 is not published"; cat out1; exit 1; }
  git -C origin.git -c gpg.ssh.allowedSignersFile="$PWD/signers" verify-commit host/desk1 2>/dev/null \
    || { echo "host/desk1 is not signed by the trusted key"; exit 1; }
  git -C origin.git ls-tree -r --name-only host/desk1 | grep -qx config/hosts/desk1/default.nix
  if git -C origin.git ls-tree -r --name-only host/desk1 | grep -q alpha; then echo "host/desk1 holds alpha"; exit 1; fi
  grep -qF "REV=$fm_rev FLAKE=" out1 || { echo "no install line"; cat out1; exit 1; }
  grep -qF "$(git -C origin.git rev-parse host/desk1)" out1 || { echo "the install line does not name host/desk1's commit"; cat out1; exit 1; }
  [ -z "$(git -C inv status --porcelain)" ] || { echo "the inventory checkout is not clean"; exit 1; }
  if git -C secrets-origin.git rev-parse -q --verify host/desk1 > /dev/null; then echo "part 2 ran"; exit 1; fi

  # The installer's side: desk1's identity and enrollment file.
  fleetmgr-secrets-sync init --state idstate --host desk1
  [ -f idstate/desk1.enroll ]

  new --from idstate/desk1.enroll --support-keys support-keys.pub > out2 2>&1 || { cat out2; exit 1; }
  git -C secrets-origin.git rev-parse -q --verify host/desk1 > /dev/null || { echo "host/desk1 of the secrets is not pushed"; cat out2; exit 1; }
  git -C secrets-origin.git -c gpg.ssh.allowedSignersFile="$PWD/signers" verify-commit host/desk1 2>/dev/null \
    || { echo "the secrets branch is not signed"; exit 1; }
  mkdir out
  git -C secrets-origin.git archive host/desk1 | tar -x -C out
  { age-keygen -y idstate/age.key; age-keygen -y maint.key; } | sort > want.recipients
  for f in hosts/desk1.yaml users/support/desk1.yaml; do
    [ -f out/$f ] || { echo "host/desk1 lacks $f"; git -C secrets-origin.git ls-tree -r --name-only host/desk1; exit 1; }
    yq '.sops.age[].recipient' out/$f | sort | diff want.recipients - \
      || { echo "$f is not encrypted to desk1 and the recovery recipient"; exit 1; }
  done
  sops -d --extract '["authorized_keys"]' out/users/support/desk1.yaml | grep -qF "$(cut -d" " -f2 support-key.pub)" \
    || { echo "support's keys are not stored"; exit 1; }
  if [ -e out/users/alice/desk1.yaml ]; then echo "alice got a file though --no-password"; exit 1; fi
  for k in secrets_deploy_key config_deploy_key; do
    grep -qF "$(cut -d' ' -f1,2 idstate/$k.pub)" registered || { echo "the register-with command did not get $k"; cat registered; exit 1; }
  done
  # The command gets each key on stdin with no newline after it.
  [ "$(grep -o ssh-ed25519 registered | wc -l)" = 2 ] || { echo "registered holds other than the two keys"; cat registered; exit 1; }

  # Later runs change nothing.
  mkdir before
  refs origin.git > before/origin
  refs secrets-origin.git > before/secrets-origin
  refs secrets > before/secrets
  refs inv > before/inv
  # The same command again, as the line a stopped run prints gives it:
  # support's keys are the ones stored, so they are left alone. Then without
  # --support-keys, which are on the branch now.
  unchanged() { # unchanged OUT: that the run that wrote OUT changed nothing
    for r in origin secrets-origin secrets inv; do
      d=$r.git
      [ -d $d ] || d=$r
      refs $d | diff before/$r - || { echo "a later run changed the refs of $r"; cat $1; exit 1; }
    done
    [ -z "$(git -C inv status --porcelain)" ] || { echo "a later run left the inventory dirty"; cat $1; exit 1; }
    [ -z "$(git -C secrets status --porcelain)" ] || { echo "a later run left the secrets dirty"; cat $1; exit 1; }
  }
  new --from idstate/desk1.enroll --support-keys support-keys.pub > out3 2>&1 || { cat out3; exit 1; }
  for want in "it is enrolled already, with this identity" "stay as they are: the same keys" "nothing to push" "an earlier run added it" "alice gets no password"; do
    grep -qF -e "$want" out3 || { echo "the third run does not say: $want"; cat out3; exit 1; }
  done
  unchanged out3
  new --from idstate/desk1.enroll > out4 2>&1 || { cat out4; exit 1; }
  grep -qF "holds them already" out4 || { echo "the fourth run does not say support's keys are there"; cat out4; exit 1; }
  unchanged out4

  echo "host new: desk1 is written, published and enrolled, and later runs change nothing"
  touch $out
''
