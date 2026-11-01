# fleetmgr, the guided command-line tool (tools/fleetmgr). Two packages:
#   static  the bare binary, built without cgo, so it is fully static on Linux
#           and needs only the system on macOS; for machines without Nix.
#   (main)  that binary wrapped with every tool it runs on its PATH, so it works
#           on any machine with Nix. nix itself is left to the machine, whose
#           daemon it has to talk to.
# The version is fixed here and the source is only tools/fleetmgr, so a host that
# installs fleetmgr rebuilds it when fleetmgr changes, not on every commit.
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
    pname = "fleetmgr-static";
    inherit version;
    src = lib.fileset.toSource {
      root = ./.;
      # runbooks for the test that they are current.
      fileset = lib.fileset.unions [ ./go.mod ./go.sum ./main.go ./flows ./internal ./runbooks ];
    };
    vendorHash = "sha256-FcI33sF4quOU6NkCxKlhghPA7ZS4aJbYZss0wRYyHgI=";
    # The setup test runs git against a temporary repository.
    nativeCheckInputs = [ git ];
    env.CGO_ENABLED = "0";
    ldflags = [ "-s" "-w" "-X main.version=${version}" ];
    meta = {
      description = "Guided procedures for this fleet: hosts, secrets, users and keys";
      mainProgram = "fleetmgr";
    };
  };

  # Everything a flow may run but nix (see flows/doctor.go).
  tools = [ git openssh sops age yq-go mkpasswd gh ];
in
symlinkJoin {
  name = "fleetmgr-${version}";
  paths = [ static ];
  nativeBuildInputs = [ makeWrapper ];
  postBuild = ''
    wrapProgram $out/bin/fleetmgr --prefix PATH : ${lib.makeBinPath tools}
  '';
  passthru = { inherit static; };
  meta = static.meta;
}
