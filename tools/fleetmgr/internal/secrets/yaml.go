package secrets

import (
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// The files the tool reads itself, rather than with yq: enrollment files,
// and the clear parts of sops files, which are their keys, their nesting and
// the recipients in their metadata (sops encrypts the values only). A value
// reads as it is written, and a mapping's keys in the order the file gives
// them, as yq prints them.

// root is a document's top mapping, or nil for an empty or null document.
func root(doc string) (*yaml.Node, error) {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		return nil, err
	}
	if len(n.Content) == 0 {
		return nil, nil
	}
	top := deref(n.Content[0])
	if isNull(top) {
		return nil, nil
	}
	if top.Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping of keys to values")
	}
	return top, nil
}

// deref follows an alias to what it names.
func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// isNull says whether a value is missing or null.
func isNull(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// get is the value of key in the mapping m; nil when m does not have the key
// or is not a mapping.
func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return deref(m.Content[i+1])
		}
	}
	return nil
}

// text is a single value as it is written; present is false when it is
// missing or null.
func text(n *yaml.Node, what string) (value string, present bool, err error) {
	switch {
	case isNull(n):
		return "", false, nil
	case n.Kind != yaml.ScalarNode:
		return "", false, fmt.Errorf("%s is not a single value", what)
	}
	return n.Value, true, nil
}

// Enrollment is what an enrollment file says: a host's, which
// fleetmgr-secrets-sync init writes, or a user's, which fleetmgr user init
// writes. A field the file does not have is empty.
type Enrollment struct {
	Version, Host, User, AgeRecipient string
	// DeployKey is secrets_deploy_pubkey, or deploy_pubkey in a file
	// written before the keys were renamed.
	DeployKey string
	// ConfigKey is config_deploy_pubkey, which files written before hosts
	// had a configuration key lack.
	ConfigKey  string
	SigningKey string
}

// ParseEnrollment reads an enrollment file. It checks only that the file is
// one; whether its fields hold what they should is for the caller to check.
func ParseEnrollment(doc string) (Enrollment, error) {
	m, err := root(doc)
	if err != nil {
		return Enrollment{}, fmt.Errorf("the enrollment file: %w", err)
	}
	var e Enrollment
	for _, f := range []struct {
		key string
		to  *string
	}{
		{"version", &e.Version}, {"host", &e.Host}, {"user", &e.User}, {"age_recipient", &e.AgeRecipient},
		{"config_deploy_pubkey", &e.ConfigKey}, {"signing_key", &e.SigningKey},
	} {
		if *f.to, _, err = text(get(m, f.key), f.key); err != nil {
			return Enrollment{}, fmt.Errorf("the enrollment file: %w", err)
		}
	}
	for _, key := range []string{"secrets_deploy_pubkey", "deploy_pubkey"} {
		v, present, err := text(get(m, key), key)
		if err != nil {
			return Enrollment{}, fmt.Errorf("the enrollment file: %w", err)
		}
		if present {
			e.DeployKey = v
			break
		}
	}
	return e, nil
}

// RecipientsIn are the age recipients a sops file is encrypted to, from its
// metadata (.sops.age[].recipient), in the order the file lists them.
func RecipientsIn(doc string) ([]string, error) {
	m, err := root(doc)
	if err != nil {
		return nil, err
	}
	age := get(get(m, "sops"), "age")
	if isNull(age) {
		return nil, nil
	}
	if age.Kind != yaml.SequenceNode {
		return nil, errors.New("sops.age is not a list")
	}
	var out []string
	for _, item := range age.Content {
		r, present, err := text(get(deref(item), "recipient"), "a recipient in sops.age")
		if err != nil {
			return nil, err
		}
		if present {
			out = append(out, r)
		}
	}
	return out, nil
}

// ListedUsers are the users a host's file lists under users, in the order
// it lists them.
func ListedUsers(doc string) ([]string, error) {
	m, err := root(doc)
	if err != nil {
		return nil, err
	}
	users := get(m, "users")
	if isNull(users) {
		return nil, nil
	}
	if users.Kind != yaml.MappingNode {
		return nil, errors.New("users is not a mapping of users")
	}
	var out []string
	for i := 0; i+1 < len(users.Content); i += 2 {
		out = append(out, users.Content[i].Value)
	}
	return out, nil
}

// HasKey says whether a file holds a value other than null at a path of
// keys: users, alice for a listed user, or the parts of a nested secret's
// name. A path through something that is not a mapping leads nowhere.
func HasKey(doc string, path ...string) (bool, error) {
	n, err := root(doc)
	if err != nil {
		return false, err
	}
	for _, key := range path {
		n = get(n, key)
	}
	return !isNull(n), nil
}

// Shape is a file as nested maps, for CheckPlace: a mapping is a
// map[string]any, null is nil, and anything else is as it is written. An
// empty file has no shape, nil.
func Shape(doc string) (map[string]any, error) {
	m, err := root(doc)
	if err != nil || m == nil {
		return nil, err
	}
	return shape(m).(map[string]any), nil
}

func shape(n *yaml.Node) any {
	n = deref(n)
	switch {
	case isNull(n):
		return nil
	case n.Kind == yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			out[n.Content[i].Value] = shape(n.Content[i+1])
		}
		return out
	case n.Kind == yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			out[i] = shape(c)
		}
		return out
	}
	return n.Value
}
