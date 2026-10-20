package config

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// setUserMu serialises config edits so two concurrent callers cannot interleave
// a read and a rename and lose one another's changes.
var setUserMu sync.Mutex

// SetUser changes one dotted key in the user config at
// <home>/.config/nova/config.yaml, creating the file and its directory when
// absent. It preserves every other key, comment, and key order, writes the
// result atomically, and refuses a key the Config schema does not define and
// any key whose name looks like a secret.
func SetUser(home, key string, value any) error {
	parts := strings.Split(key, ".")
	for _, part := range parts {
		if part == "" {
			return fmt.Errorf("config: empty segment in key %q", key)
		}
	}
	if err := refuseSecret(parts); err != nil {
		return err
	}
	if err := checkSchemaKey(parts); err != nil {
		return err
	}

	setUserMu.Lock()
	defer setUserMu.Unlock()

	base := UserConfigPath(home)
	doc, err := loadUserDoc(base)
	if err != nil {
		return err
	}
	if err := setNestedNode(doc, parts, value); err != nil {
		return err
	}
	return writeUserDoc(base+".yaml", doc)
}

// secretWords are the path segments that name credentials. A config file holds
// settings, never secrets; the auth store is the one place a key lives.
var secretWords = map[string]bool{
	"key": true, "apikey": true, "token": true,
	"secret": true, "password": true, "passwd": true,
}

// refuseSecret rejects a key whose segments name a credential. A segment is
// split on separators, so "api_key" is refused while "keybindings" is not.
func refuseSecret(parts []string) error {
	for _, part := range parts {
		for _, word := range strings.FieldsFunc(strings.ToLower(part), func(r rune) bool {
			return r == '_' || r == '-'
		}) {
			if secretWords[word] {
				return fmt.Errorf("config: refusing to write %q: secrets belong in the auth store, not the config", strings.Join(parts, "."))
			}
		}
	}
	return nil
}

// checkSchemaKey walks a dotted key against the Config struct and reports an
// unknown or non-section segment. A map-typed field accepts free sub-keys.
func checkSchemaKey(parts []string) error {
	t := reflect.TypeOf(Config{})
	for i, part := range parts {
		ft, ok := fieldType(t, part)
		if !ok {
			return fmt.Errorf("config: unknown key %q", strings.Join(parts[:i+1], "."))
		}
		t = indirect(ft)
		if i == len(parts)-1 {
			return nil
		}
		switch t.Kind() {
		case reflect.Map:
			return nil
		case reflect.Struct:
			continue
		default:
			return fmt.Errorf("config: %q is not a section", strings.Join(parts[:i+1], "."))
		}
	}
	return nil
}

// loadUserDoc reads the user config at base (config.yaml, else config.json, the
// same choice Load makes) into a YAML document node. Starting from the JSON file
// matters: SetUser writes config.yaml, which Load prefers, so writing a fresh
// YAML file beside an existing config.json would hide all of its settings. No
// file yields an empty mapping document. A file that is not valid YAML, or whose
// root is not a mapping, is an error so a corrupt file is never overwritten.
func loadUserDoc(base string) (*yaml.Node, error) {
	data, path, err := readFirst(readConfigFile, base)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if path == "" {
		return emptyDoc(), nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind == 0 {
		return emptyDoc(), nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root.Kind = yaml.MappingNode
		root.Tag = "!!map"
		root.Value = ""
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config: %s: root is not a mapping", path)
	}
	if filepath.Ext(path) == ".json" {
		clearStyles(root)
	}
	return &doc, nil
}

// clearStyles drops the flow and quoting styles a JSON file carries, so the
// YAML written from it reads as block YAML.
func clearStyles(n *yaml.Node) {
	n.Style = 0
	for _, child := range n.Content {
		clearStyles(child)
	}
}

// emptyDoc returns a document holding an empty mapping.
func emptyDoc() *yaml.Node {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
}

// setNestedNode writes value at the dotted path inside doc, creating
// intermediate mappings. Existing keys keep their position and comments.
func setNestedNode(doc *yaml.Node, parts []string, value any) error {
	vn, err := valueNode(value)
	if err != nil {
		return err
	}
	mapping := doc.Content[0]
	for i, part := range parts {
		keyAt, valAt := findKey(mapping, part)
		if i == len(parts)-1 {
			if keyAt < 0 {
				mapping.Content = append(mapping.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}, vn)
			} else {
				mapping.Content[valAt] = vn
			}
			return nil
		}
		if keyAt < 0 {
			child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapping.Content = append(mapping.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}, child)
			mapping = child
			continue
		}
		child := mapping.Content[valAt]
		if child.Kind != yaml.MappingNode {
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapping.Content[valAt] = child
		}
		mapping = child
	}
	return nil
}

// findKey returns the indices of the key and value nodes for name, or -1 each.
func findKey(mapping *yaml.Node, name string) (keyAt, valAt int) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return i, i + 1
		}
	}
	return -1, -1
}

// valueNode encodes a Go value as a YAML node so it renders with the right tag.
func valueNode(value any) (*yaml.Node, error) {
	data, err := yaml.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("config: encode value: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("config: encode value: %w", err)
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return doc.Content[0], nil
}

// writeUserDoc encodes doc and replaces path atomically, so a crash mid-write
// never truncates the live config.
func writeUserDoc(path string, doc *yaml.Node) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("config: encode %s: %w", path, err)
	}
	return writeFileAtomic(path, buf.Bytes(), "user config")
}
