package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Fingerprint hashes the project trust surface: o-.yaml + go.mod + go.sum.
// Used both for the --trust model (Security condition) and the build cache key.
func Fingerprint(dir string) (string, error) {
	h := sha256.New()
	for _, name := range []string{"o-.yaml", "go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CacheDir returns $XDG_CACHE_HOME/o- (or ~/.cache/o-), created with mode 0700
// (Security condition: no world-readable build artifacts).
func CacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	d := filepath.Join(base, "o-")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

type trustStore struct {
	Dirs map[string]string `json:"dirs"` // project dir -> fingerprint
}

func loadTrust() (map[string]string, error) {
	cd, err := CacheDir()
	if err != nil {
		return nil, err
	}
	p := filepath.Join(cd, "trust.json")
	// Trust-gate DoS guard (Security finding, 2026-08-25): a symlinked
	// trust.json (e.g. -> /dev/random) would block os.ReadFile forever. Lstat
	// rejects symlinks outright; an oversized file is treated as corrupt.
	fi, err := os.Lstat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || fi.Size() > 1<<20 {
		return map[string]string{}, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var ts trustStore
	if err := json.Unmarshal(data, &ts); err != nil {
		// Corrupt trust file: never fail closed on it, start fresh.
		return map[string]string{}, nil
	}
	if ts.Dirs == nil {
		ts.Dirs = map[string]string{}
	}
	return ts.Dirs, nil
}

func saveTrust(dirs map[string]string) error {
	cd, err := CacheDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(trustStore{Dirs: dirs}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cd, "trust.json"), data, 0o600)
}

// Trusted reports whether dir's current fingerprint matches the stored one.
func Trusted(dir string) (bool, error) {
	fp, err := Fingerprint(dir)
	if err != nil {
		return false, err
	}
	dirs, err := loadTrust()
	if err != nil {
		return false, err
	}
	stored, ok := dirs[dir]
	return ok && stored == fp, nil
}

// Trust records dir's current fingerprint as trusted.
func Trust(dir string) error {
	fp, err := Fingerprint(dir)
	if err != nil {
		return err
	}
	dirs, err := loadTrust()
	if err != nil {
		return err
	}
	dirs[dir] = fp
	return saveTrust(dirs)
}
