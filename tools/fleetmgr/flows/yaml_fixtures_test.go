package flows

import (
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/secrets"
)

// The files the fakes answer with, now that the tool reads YAML itself
// rather than asking yq: sops files as sops writes them, their keys in
// clear, their values encrypted, their recipients in the metadata.

// sopsFile is a sops file holding keys, a / in a key nesting it, and
// encrypted to recipients. Its first line, a comment, names owner, so that a
// fake answering per host or per user can tell whose file it was given
// (ownerOf).
func sopsFile(owner string, keys []string, recipients ...string) string {
	var tree yamlTree
	for _, k := range keys {
		tree.add(strings.Split(k, "/"))
	}
	var b strings.Builder
	b.WriteString("# " + owner + "\n")
	tree.write(&b, "")
	if len(recipients) > 0 {
		b.WriteString("sops:\n    age:\n")
		for _, r := range recipients {
			b.WriteString("        - recipient: " + r + "\n" +
				"          enc: |\n" +
				"            -----BEGIN AGE ENCRYPTED FILE-----\n" +
				"            YWdl\n" +
				"            -----END AGE ENCRYPTED FILE-----\n")
		}
		b.WriteString("    version: 3.12.2\n")
	}
	return b.String()
}

// ownerOf is the owner a sopsFile names on its first line, or "".
func ownerOf(doc string) string {
	first, _, _ := strings.Cut(doc, "\n")
	name, ok := strings.CutPrefix(first, "# ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(name)
}

// yamlTree is keys nested in the order they were added; a key with no keys
// under it is a value.
type yamlTree struct {
	keys []string
	kids map[string]*yamlTree
}

func (t *yamlTree) add(path []string) {
	if len(path) == 0 {
		return
	}
	if t.kids == nil {
		t.kids = map[string]*yamlTree{}
	}
	kid, ok := t.kids[path[0]]
	if !ok {
		kid = &yamlTree{}
		t.kids[path[0]] = kid
		t.keys = append(t.keys, path[0])
	}
	kid.add(path[1:])
}

func (t *yamlTree) write(b *strings.Builder, indent string) {
	for _, k := range t.keys {
		kid := t.kids[k]
		if len(kid.keys) == 0 {
			b.WriteString(indent + k + ": ENC[AES256_GCM,data:aGk=,iv:aXY=,tag:dGFn,type:str]\n")
			continue
		}
		b.WriteString(indent + k + ":\n")
		kid.write(b, indent+"    ")
	}
}

func TestSopsFileReadsAsTheToolReadsIt(t *testing.T) {
	doc := sopsFile("nas", []string{"wifi_psk", "hotspot/wls0", "users/bob", "users/alice"}, "age1hostrecipient", "age1recoveryrecipient")
	if got := ownerOf(doc); got != "nas" {
		t.Errorf("owner %q", got)
	}
	if r, err := secrets.RecipientsIn(doc); err != nil || strings.Join(r, " ") != "age1hostrecipient age1recoveryrecipient" {
		t.Errorf("recipients %q, %v", r, err)
	}
	if u, err := secrets.ListedUsers(doc); err != nil || strings.Join(u, " ") != "bob alice" {
		t.Errorf("users %q, %v", u, err)
	}
	if has, err := secrets.HasKey(doc, "hotspot", "wls0"); err != nil || !has {
		t.Errorf("hotspot/wls0: %v, %v", has, err)
	}
	if empty := sopsFile("nas", nil); ownerOf(empty) != "nas" {
		t.Errorf("a file with nothing in it: %q", empty)
	}
}
