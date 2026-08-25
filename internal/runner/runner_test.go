package runner

import (
	"reflect"
	"testing"
)

func TestSanitizeEnv_DropsSecretsAndInjections(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		"HOME=/home/x",
		"O+_INTERNAL=secret",
		"O+_CACHE=/tmp/o+",
		"LD_PRELOAD=/tmp/evil.so",
		"LD_LIBRARY_PATH=/tmp/evil",
	}
	got := sanitizeEnv(env)
	want := []string{"PATH=/usr/bin", "HOME=/home/x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sanitizeEnv = %v, want %v", got, want)
	}
}

func TestSanitizeEnv_KeepsNormalVars(t *testing.T) {
	env := []string{"HOME=/home/x", "PORT=8080", "DATABASE_URL=postgres://x"}
	got := sanitizeEnv(env)
	if len(got) != 3 {
		t.Errorf("normal env must pass through untouched, got %v", got)
	}
}

func TestExited_NilRunner(t *testing.T) {
	var r *Runner
	if !r.Exited() {
		t.Error("nil runner must report exited")
	}
}

func TestPID_NilRunner(t *testing.T) {
	var r *Runner
	if r.PID() != 0 {
		t.Errorf("nil runner PID = %d, want 0", r.PID())
	}
}
