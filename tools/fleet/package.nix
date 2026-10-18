# fleet, the guided command-line tool (tools/fleet). Two packages:
#   static  the bare binary, built without cgo, so it is fully static on Linux
#           and needs only the system on macOS; for machines without Nix.
#   (main)  that binary wrapped with every tool it runs on its PATH, so it works
#           on any machine with Nix. nix itself is left to the machine, whose
#           daemon it has to talk to.
# The version is fixed here and the source is only tools/fleet, so a host that
# installs fleet rebuilds it when fleet changes, not on every commit.
{
  lib,
  buildGoModule,
  makeWrapper,
  symlinkJoin,
  git,
  openssh,
  sops,
  age,
  yq-go,
  mkpasswd,
  gh,
  version ? "0.1.0",
}:
let
  static = buildGoModule {
    pname = "fleet-static";
    inherit version;
    src = lib.fileset.toSource {
      root = ./.;
      # runbooks for the test that they are current.
      fileset = lib.fileset.unions [ ./go.mod ./go.sum ./main.go ./flows ./internal ./runbooks ];
    };
    vendorHash = "sha256-nlulmgHFiI8/r6t5Dh6Ze0OxEW91Be4YfE5X2Id6+UM=";
    env.CGO_ENABLED = "0";
    ldflags = [ "-s" "-w" "-X main.version=${version}" ];
    meta = {
      description = "Guided procedures for this fleet: hosts, secrets, users and keys";
      mainProgram = "fleet";
    };
  };

  # Everything a flow may run but nix (see flows/doctor.go).
  tools = [ git openssh sops age yq-go mkpasswd gh ];
in
symlinkJoin {
  name = "fleet-${version}";
  paths = [ static ];
  nativeBuildInputs = [ makeWrapper ];
  postBuild = ''
    wrapProgram $out/bin/fleet --prefix PATH : ${lib.makeBinPath tools}
  '';
  passthru = { inherit static; };
  meta = static.meta;
}
