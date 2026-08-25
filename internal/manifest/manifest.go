package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// MaxManifestSize caps o-.yaml at 1MB (Security condition: no unbounded input).
	MaxManifestSize = 1 << 20
	// MaxDepth caps nesting at 64 (Security condition: resource exhaustion guard).
	MaxDepth = 64
)

type Build struct {
	Output  string   `yaml:"output"`
	Ldflags []string `yaml:"ldflags"`
	Tags    []string `yaml:"tags"`
	Static  *bool    `yaml:"static"`
}

type Run struct {
	Watch   []string `yaml:"watch"`
	Exclude []string `yaml:"exclude"`
	PreRun  []string `yaml:"pre_run"`
}

type Test struct {
	Tags    []string `yaml:"tags"`
	Timeout string   `yaml:"timeout"`
}

type Bundle struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
	MaxSize int64    `yaml:"max_size"`
}

type Manifest struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
	Type    string `yaml:"type"`
	Build   Build  `yaml:"build"`
	Run     Run    `yaml:"run"`
	Test    Test   `yaml:"test"`
	Bundle  Bundle `yaml:"bundle"`
}

// Default returns the zero-config manifest for a directory.
func Default(dir string) *Manifest {
	return &Manifest{
		Name: filepath.Base(dir),
		Type: "app",
	}
}

func (m *Manifest) fillDefaults(dir string) {
	if m.Name == "" {
		m.Name = filepath.Base(dir)
	}
	if m.Type == "" {
		m.Type = "app"
	}
}

// Load reads and strictly parses o-.yaml in dir. Returns the default manifest
// when the file is absent. Safety: 1MB cap, nesting depth limit 64, anchors and
// aliases forbidden (billion-laughs protection), unknown fields rejected.
func Load(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "o-.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(dir), nil
		}
		return nil, err
	}
	if len(data) > MaxManifestSize {
		return nil, errors.New("o-.yaml exceeds 1MB size limit")
	}

	// Pass 1: parse to a node tree for safety checks (no alias expansion here).
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("invalid o-.yaml: %w", err)
	}
	if err := checkDepth(&root, 0); err != nil {
		return nil, err
	}
	if err := rejectAliases(&root); err != nil {
		return nil, err
	}

	// Pass 2: strict decode into the struct (aliases already ruled out, so no
	// expansion bomb can trigger here).
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid o-.yaml: %w", err)
	}
	m.fillDefaults(dir)
	return &m, nil
}

func checkDepth(n *yaml.Node, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("o-.yaml nesting exceeds depth limit %d", MaxDepth)
	}
	for _, c := range n.Content {
		if err := checkDepth(c, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func rejectAliases(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode {
		return errors.New("o-.yaml anchors/aliases are forbidden")
	}
	for _, c := range n.Content {
		if err := rejectAliases(c); err != nil {
			return err
		}
	}
	return nil
}
