package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deepYAML builds a nested YAML document `levels` deep.
func deepYAML(levels int) string {
	var sb strings.Builder
	for i := 0; i < levels; i++ {
		sb.WriteString(strings.Repeat("  ", i))
		sb.WriteString("k:\n")
	}
	sb.WriteString(strings.Repeat("  ", levels))
	sb.WriteString("leaf: 1\n")
	return sb.String()
}

func writeManifest(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "o-.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("# comment\n", (1<<20)/10+10) // > 1MB
	if err := os.WriteFile(filepath.Join(dir, "o-.yaml"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "1MB") {
		t.Fatalf("expected 1MB size-limit error, got %v", err)
	}
}

func TestLoad_DepthLimit(t *testing.T) {
	dir := writeManifest(t, deepYAML(70))
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("expected depth-limit error, got %v", err)
	}
}

func TestLoad_AliasRejected(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"simple anchor", "app: &app\n  name: foo\nother: *app"},
		{"nested alias", "a: &a {x: 1}\nb: *a"},
		{"chain anchors", "a: &a {x: 1}\nb: &b {y: *a}\nc: *b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeManifest(t, tc.yaml)
			_, err := Load(dir)
			if err == nil || !strings.Contains(err.Error(), "anchor") {
				t.Fatalf("expected anchor/alias error, got %v", err)
			}
		})
	}
}

func TestLoad_UnknownFields(t *testing.T) {
	dir := writeManifest(t, "name: svc\nbogus_key: true\n")
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected unknown-field error, got nil")
	}
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != filepath.Base(dir) {
		t.Errorf("default name = %q, want %q", m.Name, filepath.Base(dir))
	}
	if m.Type != "app" {
		t.Errorf("default type = %q, want app", m.Type)
	}
}

func TestLoad_ValidManifest(t *testing.T) {
	dir := writeManifest(t, "name: svc\nversion: \"1.0.0\"\ntype: app\nbuild:\n  output: ./dist/svc\nrun:\n  watch:\n    - ./**/*.go\n")
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "svc" || m.Version != "1.0.0" || m.Type != "app" {
		t.Errorf("unexpected manifest: %+v", m)
	}
	if m.Build.Output != "./dist/svc" {
		t.Errorf("build.output = %q", m.Build.Output)
	}
	if len(m.Run.Watch) != 1 || m.Run.Watch[0] != "./**/*.go" {
		t.Errorf("run.watch = %v", m.Run.Watch)
	}
}

func TestLoad_EmptyManifestFillsDefaults(t *testing.T) {
	dir := writeManifest(t, "version: \"2.0.0\"\n")
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != filepath.Base(dir) {
		t.Errorf("name should default to dir base, got %q", m.Name)
	}
	if m.Version != "2.0.0" {
		t.Errorf("version = %q", m.Version)
	}
}
