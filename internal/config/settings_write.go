package config

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SetValues writes the settings page's values into the file at path, each
// under its dotted key ("workers.effort"), creating the sections a key needs
// and keeping every other key and comment as they were. An empty string, a
// zero number or nil removes the key, leaving the default to apply, the way
// the omitted keys of Save do. The result is checked like any write: a value
// or key the configuration refuses leaves the file as it was.
func SetValues(path string, values map[string]any) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("config %s: %w", path, describeYAMLError(err))
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config %s: the top level is not a mapping", path)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, key := range keys {
		leaf, err := scalarFor(values[key])
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		setPath(root, strings.Split(key, "."), leaf)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(4)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeCheckedConfig(path, out.Bytes(), "the settings")
}

// scalarFor is v as a YAML scalar, or nil for a value that removes its key.
func scalarFor(v any) (*yaml.Node, error) {
	switch v := v.(type) {
	case nil:
		return nil, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v)}, nil
	case int:
		if v == 0 {
			return nil, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(v)}, nil
	case []string:
		if len(v) == 0 {
			return nil, nil
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, item := range v {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item})
		}
		return seq, nil
	}
	return nil, fmt.Errorf("a %T is not a setting's value", v)
}

// setPath puts leaf at path under m, or removes the key when leaf is nil,
// and reports whether m is left empty.
func setPath(m *yaml.Node, path []string, leaf *yaml.Node) bool {
	i := slices.IndexFunc(pairs(m), func(k *yaml.Node) bool { return k.Value == path[0] })
	if len(path) == 1 {
		switch {
		case leaf == nil && i >= 0:
			m.Content = slices.Delete(m.Content, 2*i, 2*i+2)
		case leaf != nil && i >= 0:
			old := m.Content[2*i+1]
			leaf.LineComment, leaf.HeadComment, leaf.FootComment = old.LineComment, old.HeadComment, old.FootComment
			m.Content[2*i+1] = leaf
		case leaf != nil:
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: path[0]}, leaf)
		}
		return len(m.Content) == 0
	}
	if i < 0 {
		if leaf == nil {
			return len(m.Content) == 0
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: path[0]}, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		i = len(m.Content)/2 - 1
	}
	child := m.Content[2*i+1]
	if child.Kind != yaml.MappingNode {
		// A section written as null or {} is replaced by the mapping it needs.
		child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		m.Content[2*i+1] = child
	}
	if setPath(child, path[1:], leaf) {
		m.Content = slices.Delete(m.Content, 2*i, 2*i+2)
	}
	return len(m.Content) == 0
}

// pairs is a mapping's keys, in order.
func pairs(m *yaml.Node) []*yaml.Node {
	keys := make([]*yaml.Node, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i])
	}
	return keys
}
