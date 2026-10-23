package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoIDIsTheSameHoweverTheAddressIsWritten(t *testing.T) {
	for _, a := range []string{
		"git@github.com:Example-Org/inventory.git",
		"ssh://git@github.com/Example-Org/inventory",
		"ssh://git@github.com:22/Example-Org/inventory.git",
		"https://github.com/Example-Org/inventory.git/",
		"git+ssh://git@GitHub.com/Example-Org/inventory?rev=1111111111111111111111111111111111111111",
		"git+https://github.com/Example-Org/inventory",
	} {
		id, err := RepoID(a)
		if err != nil || id != "github.com/Example-Org/inventory" {
			t.Errorf("RepoID(%q) = %q, %v", a, id, err)
		}
	}
	for _, bad := range []string{"", "inventory", "https://github.com/../etc", "/tmp/repo"} {
		if id, err := RepoID(bad); err == nil {
			t.Errorf("RepoID(%q) = %q, want an error", bad, id)
		}
	}
}

func TestSignersCopyLivesInTheStateFolder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	p, err := SignersCopy("git@github.com:o/r.git")
	if err != nil || p != "/state/fleet/repos/github.com/o/r/allowed_signers" {
		t.Errorf("SignersCopy = %q, %v", p, err)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/me")
	if p, _ := SignersCopy("git@github.com:o/r.git"); p != "/home/me/.local/state/fleet/repos/github.com/o/r/allowed_signers" {
		t.Errorf("without XDG_STATE_HOME: %q", p)
	}
}

// A key made for this test, with the fingerprint ssh-keygen -l
// prints for it.
const testKey = "AAAAC3NzaC1lZDI1NTE5AAAAIB4cTxXwY1G40AfKlCx6sZEOmkWK4TNdbOHgtpKe+Qc7"

func TestParseSignersReadsKeysAndFingerprints(t *testing.T) {
	data := []byte("# trusted\n\nme@test namespaces=\"git\" ssh-ed25519 " + testKey + "\n" +
		"other cert-authority,namespaces=\"git file\" ssh-ed25519 " + testKey + " comment\n")
	keys, err := ParseSigners(data)
	if err != nil || len(keys) != 2 {
		t.Fatalf("%v %+v", err, keys)
	}
	if keys[0].Label != "me@test" || keys[0].Type != "ssh-ed25519" || keys[0].Blob != testKey {
		t.Errorf("%+v", keys[0])
	}
	if keys[0].Fingerprint != "SHA256:8MIxN7gegMpbo32xsB9SnhR8FmxFSqyhNhTrsj8D5I4" {
		t.Errorf("fingerprint %s", keys[0].Fingerprint)
	}
	if keys[1].Label != "other" {
		t.Errorf("a quoted option with a space split the line: %+v", keys[1])
	}
	if _, err := ParseSigners([]byte("me@test namespaces=\"git\"\n")); err == nil {
		t.Error("a line without a key was accepted")
	}
}

func TestDiffSignersComparesKeysNotLabels(t *testing.T) {
	a := []SignerKey{{Label: "me", Type: "ssh-ed25519", Blob: "A"}, {Label: "old", Type: "ssh-ed25519", Blob: "B"}}
	b := []SignerKey{{Label: "renamed", Type: "ssh-ed25519", Blob: "A"}, {Label: "new", Type: "ssh-ed25519", Blob: "C"}}
	added, removed := DiffSigners(a, b)
	if len(added) != 1 || added[0].Blob != "C" || len(removed) != 1 || removed[0].Blob != "B" {
		t.Errorf("added %+v removed %+v", added, removed)
	}
}

func TestTrustedSignersSaysWhenThereIsNoCopy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	if _, _, err := TrustedSigners("git@github.com:o/r.git"); err != ErrNoSignersCopy {
		t.Fatalf("err = %v", err)
	}
	path, _ := SignersCopy("git@github.com:o/r.git")
	if err := WriteSignersCopy(path, []byte("me ssh-ed25519 "+testKey+"\n")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", st.Mode().Perm())
	}
	if _, keys, err := TrustedSigners("https://github.com/o/r"); err != nil || len(keys) != 1 {
		t.Errorf("%v %+v", err, keys)
	}
}
