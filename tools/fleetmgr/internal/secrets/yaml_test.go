package secrets

import (
	"slices"
	"strings"
	"testing"
)

// A host's file as sops writes it: the keys in clear, the values encrypted.
const hostFile = `wifi_psk: ENC[AES256_GCM,data:aGk=,iv:aXY=,tag:dGFn,type:str]
hotspot:
    wls1: ENC[AES256_GCM,data:aGk=,iv:aXY=,tag:dGFn,type:str]
    wls0: ENC[AES256_GCM,data:aGk=,iv:aXY=,tag:dGFn,type:str]
users:
    bob: ENC[AES256_GCM,data:aGk=,iv:aXY=,tag:dGFn,type:str]
    alice: ENC[AES256_GCM,data:aGk=,iv:aXY=,tag:dGFn,type:str]
    carol: null
sops:
    age:
        - recipient: age1host
          enc: |
            -----BEGIN AGE ENCRYPTED FILE-----
            YWdl
            -----END AGE ENCRYPTED FILE-----
        - recipient: age1recovery
          enc: |
            -----BEGIN AGE ENCRYPTED FILE-----
            YWdl
            -----END AGE ENCRYPTED FILE-----
    lastmodified: "2026-10-18T00:00:00Z"
    mac: ENC[AES256_GCM,data:bWFj,iv:aXY=,tag:dGFn,type:str]
    version: 3.12.2
`

func TestParseEnrollment(t *testing.T) {
	for name, c := range map[string]struct {
		doc  string
		want Enrollment
	}{
		"a host's": {
			"version: 1\nhost: pi\ncreated: 2026-10-18T00:00:00Z\nage_recipient: age1abc\n" +
				"secrets_deploy_pubkey: ssh-ed25519 AAAA fleet-secrets@pi\nconfig_deploy_pubkey: ssh-ed25519 BBBB fleet-config@pi\n",
			Enrollment{Version: "1", Host: "pi", AgeRecipient: "age1abc", DeployKey: "ssh-ed25519 AAAA fleet-secrets@pi", ConfigKey: "ssh-ed25519 BBBB fleet-config@pi"},
		},
		"a host's from before the keys were renamed": {
			"version: 1\nhost: pi\nage_recipient: age1abc\ndeploy_pubkey: ssh-ed25519 AAAA fleet-deploy@pi\n",
			Enrollment{Version: "1", Host: "pi", AgeRecipient: "age1abc", DeployKey: "ssh-ed25519 AAAA fleet-deploy@pi"},
		},
		"the new key wins over the old": {
			"version: 1\nhost: pi\nsecrets_deploy_pubkey: ssh-ed25519 NEW\ndeploy_pubkey: ssh-ed25519 OLD\n",
			Enrollment{Version: "1", Host: "pi", DeployKey: "ssh-ed25519 NEW"},
		},
		"a null new key falls back to the old": {
			"version: 1\nsecrets_deploy_pubkey: ~\ndeploy_pubkey: ssh-ed25519 OLD\n",
			Enrollment{Version: "1", DeployKey: "ssh-ed25519 OLD"},
		},
		"a user's": {
			"version: 1\nuser: dave\nage_recipient: age1dave\nsigning_key: ssh-ed25519 CCCC dave fleet signing\n",
			Enrollment{Version: "1", User: "dave", AgeRecipient: "age1dave", SigningKey: "ssh-ed25519 CCCC dave fleet signing"},
		},
		"a quoted version": {`version: "1"`, Enrollment{Version: "1"}},
		"a null field":     {"version: 1\nconfig_deploy_pubkey: null\n", Enrollment{Version: "1"}},
		"an empty file":    {"", Enrollment{}},
	} {
		got, err := ParseEnrollment(c.doc)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		} else if got != c.want {
			t.Errorf("%s: got %+v, want %+v", name, got, c.want)
		}
	}
	for name, doc := range map[string]string{
		"not YAML":           "version: [1\n",
		"not a mapping":      "just some text\n",
		"a list for a value": "version: 1\nage_recipient: [age1a, age1b]\n",
	} {
		if e, err := ParseEnrollment(doc); err == nil {
			t.Errorf("%s: read as %+v", name, e)
		}
	}
}

func TestRecipientsIn(t *testing.T) {
	got, err := RecipientsIn(hostFile)
	if err != nil || !slices.Equal(got, []string{"age1host", "age1recovery"}) {
		t.Errorf("got %q, %v; want the host's and the recovery recipient, in order", got, err)
	}
	for name, doc := range map[string]string{"no sops metadata": "wifi_psk: ENC[…]\n", "an empty file": ""} {
		if got, err := RecipientsIn(doc); err != nil || len(got) != 0 {
			t.Errorf("%s: got %q, %v; want none", name, got, err)
		}
	}
	if _, err := RecipientsIn("sops:\n    age: age1host\n"); err == nil {
		t.Error("sops.age that is not a list was read")
	}
}

func TestListedUsers(t *testing.T) {
	got, err := ListedUsers(hostFile)
	if err != nil || !slices.Equal(got, []string{"bob", "alice", "carol"}) {
		t.Errorf("got %q, %v; want the users in the file's order", got, err)
	}
	for name, doc := range map[string]string{"no users": "wifi_psk: ENC[…]\n", "users: null": "users: null\n"} {
		if got, err := ListedUsers(doc); err != nil || len(got) != 0 {
			t.Errorf("%s: got %q, %v; want none", name, got, err)
		}
	}
	if _, err := ListedUsers("users: [alice]\n"); err == nil {
		t.Error("users that is not a mapping was read")
	}
}

func TestHasKey(t *testing.T) {
	for path, want := range map[string]bool{
		"users/alice":       true,
		"users/dave":        false,
		"users/carol":       false, // null
		"hotspot/wls0":      true,
		"hotspot/wls2":      false,
		"hotspot":           true,
		"wifi_psk":          true,
		"wifi_psk/home":     false, // under a value
		"hotspot/wls0/deep": false,
		"wireguard/wg0":     false,
	} {
		if got, err := HasKey(hostFile, strings.Split(path, "/")...); err != nil || got != want {
			t.Errorf("%s: got %v, %v; want %v", path, got, err, want)
		}
	}
	if got, err := HasKey("", "users", "alice"); err != nil || got {
		t.Errorf("an empty file: got %v, %v", got, err)
	}
	if _, err := HasKey("users: {alice\n", "users", "alice"); err == nil {
		t.Error("a file that is not YAML was read")
	}
}

func TestShapeIsWhatCheckPlaceReads(t *testing.T) {
	doc, err := Shape(hostFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPlace(doc, "hotspot"); err == nil || !strings.Contains(err.Error(), "hotspot holds secrets of its own (wls0, wls1)") {
		t.Errorf("hotspot: got %v", err)
	}
	if err := CheckPlace(doc, "wifi_psk/home"); err == nil || !strings.Contains(err.Error(), "wifi_psk holds a value") {
		t.Errorf("wifi_psk/home: got %v", err)
	}
	if err := CheckPlace(doc, "hotspot/wls2"); err != nil {
		t.Errorf("hotspot/wls2 refused: %v", err)
	}
	if doc, err := Shape(""); err != nil || doc != nil {
		t.Errorf("an empty file: got %v, %v; want no shape", doc, err)
	}
	if doc, err := Shape("a: null\nb:\n    c: [1, 2]\n"); err != nil || doc["a"] != nil {
		t.Errorf("got %v, %v; want a null value as nil", doc, err)
	}
}
