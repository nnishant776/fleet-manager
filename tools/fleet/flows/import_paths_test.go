package flows

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

func TestFindPathLiteralsSkipsCommentsAndStrings(t *testing.T) {
	src := `{ ... }: {
  imports = [ ./hardware.nix ../common/x.nix ];
  # imports = [ ./commented.nix ];
  /* ./block.nix */
  a = "./in-a-string.nix";
  b = ''
    ./in-an-indented-string.nix
  '';
  c = ../../fleet/allowed_signers; # ./after-code.nix
  d = x.y./notapath;
  e = ./.;
}
`
	got := findPathLiterals(src)
	want := []string{"./hardware.nix", "../common/x.nix", "../../fleet/allowed_signers", "./."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("found %q, want %q", got, want)
	}
}

func testRelocation() relocation {
	return relocation{Host: "mini", HostsDir: "config/hosts", UsersDir: "config/users", Users: map[string]bool{"alice": true}}
}

func TestRewriteDepthOfTheSignersPath(t *testing.T) {
	r := testRelocation()
	cases := []struct{ file, in, want string }{
		{"hosts/mini/default.nix", "x = ../../fleet/allowed_signers;", "x = ../../../fleet/allowed_signers;"},
		{"hosts/mini/vms/x.nix", "x = ../../../fleet/allowed_signers;", "x = ../../../../fleet/allowed_signers;"},
	}
	for _, c := range cases {
		got, outside := rewritePathLiterals(c.in, c.file, r)
		if got != c.want || len(outside) != 0 {
			t.Errorf("%s: %q, outside %q; want %q", c.file, got, outside, c.want)
		}
	}
}

func TestRewriteLeavesPathsInsideTheFolderAlone(t *testing.T) {
	src := "imports = [ ./hardware.nix ./vms/x.nix ../mini/y.nix ];"
	got, outside := rewritePathLiterals(src, "hosts/mini/default.nix", testRelocation())
	if got != src || len(outside) != 0 {
		t.Errorf("changed %q, outside %q", got, outside)
	}
}

func TestRewriteMovesAUsersFolder(t *testing.T) {
	r := testRelocation()
	got, outside := rewritePathLiterals("u = ../../users/alice/home.nix;", "hosts/mini/default.nix", r)
	if got != "u = ../../users/alice/home.nix;" || len(outside) != 0 {
		t.Errorf("same depth should stay: %q %q", got, outside)
	}
	r.UsersDir = "people/users"
	got, _ = rewritePathLiterals("u = ../../users/alice/home.nix;", "hosts/mini/default.nix", r)
	if want := "u = ../../../people/users/alice/home.nix;"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// And from the user's folder to the signers file.
	got, _ = rewritePathLiterals("k = ../../fleet/allowed_signers;", "users/alice/default.nix", r)
	if want := "k = ../../../fleet/allowed_signers;"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteReportsPathsOutsideTheFolder(t *testing.T) {
	r := testRelocation()
	src := "a = ../../shared/x.nix; b = ../other/y.nix; c = ../../users/bob/z.nix; d = ../../../up.nix;"
	got, outside := rewritePathLiterals(src, "hosts/mini/default.nix", r)
	want := []string{"../../shared/x.nix", "../other/y.nix", "../../users/bob/z.nix", "../../../up.nix"}
	if !reflect.DeepEqual(outside, want) {
		t.Errorf("outside %q, want %q", outside, want)
	}
	if got != src {
		t.Errorf("changed the text: %q", got)
	}
}

func TestReplaceAddress(t *testing.T) {
	fm, _ := fleet.RepoID(fleet.UpstreamURL)
	const inv = "ssh://git@forge/inventory.git"
	for _, a := range []string{
		"https://github.com/nnishant776/fleet-manager",
		"https://github.com/nnishant776/fleet-manager.git",
		"git@github.com:nnishant776/fleet-manager.git",
		"ssh://git@github.com/nnishant776/fleet-manager.git",
	} {
		got, n := replaceAddress(`url = "`+a+`";`, fm, inv)
		if want := `url = "` + inv + `";`; got != want || n != 1 {
			t.Errorf("%s: %q (%d)", a, got, n)
		}
	}
	other := `a = "https://github.com/example-org/other"; # "https://github.com/nnishant776/fleet-manager"
b = "plain"; c = "git@github.com:example-org/inventory.git";`
	if got, n := replaceAddress(other, fm, inv); got != other || n != 0 {
		t.Errorf("changed another repository's address or a comment: %q (%d)", got, n)
	}
}

func TestAddressFromOrigin(t *testing.T) {
	for origin, want := range map[string]string{
		"https://forge.example/org/inventory.git":   "git@forge.example:org/inventory.git",
		"https://forge.example/org/inventory":       "git@forge.example:org/inventory.git",
		"ssh://git@forge.example/org/inventory.git": "ssh://git@forge.example/org/inventory.git",
		"git@forge.example:org/inventory.git":       "git@forge.example:org/inventory.git",
	} {
		got, err := addressFromOrigin(origin)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", origin, got, err, want)
		}
	}
	for _, origin := range []string{"file:///srv/inventory.git", "/srv/inventory.git", "https://forge.example/inventory"} {
		if got, err := addressFromOrigin(origin); err == nil || !strings.Contains(err.Error(), "give --address") {
			t.Errorf("%s: %q, %v; want an error", origin, got, err)
		}
	}
}
