// Package config loads and saves ~/.config/kobosync/config.toml.
//
// Only flat `key = "value"` lines are supported, which is all kobosync needs.
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Library  string // folder with the EPUB library
	KoboDir  string // folder on the Kobo that synced books go into
	Mount    string // optional fixed Kobo mount point; autodetected when empty
	Language string // interface language, e.g. "en" or "de"
}

func Default() Config {
	return Config{KoboDir: "Books", Language: "en"}
}

// Dir is the kobosync config folder.
func Dir() string {
	return filepath.Dir(Path())
}

// Path returns the location of the config file.
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "kobosync", "config.toml")
}

// CachePath returns the location of the metadata cache.
func CachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(dir, "kobosync", "metadata.json")
}

// Load reads the config file; a missing file yields the defaults.
func Load() (Config, error) {
	c := Default()
	f, err := os.Open(Path())
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	defer f.Close()
	kv, err := ParseKV(f)
	if err != nil {
		return c, fmt.Errorf("%s: %w", Path(), err)
	}
	if v, ok := kv["library"]; ok {
		c.Library = ExpandHome(v)
	}
	if v, ok := kv["kobo_dir"]; ok {
		c.KoboDir = strings.Trim(v, "/")
	}
	if v, ok := kv["mount"]; ok {
		c.Mount = ExpandHome(v)
	}
	if v, ok := kv["language"]; ok && v != "" {
		c.Language = v
	}
	return c, nil
}

// ParseKV reads flat `key = "value"` lines; # starts a comment line.
func ParseKV(r io.Reader) (map[string]string, error) {
	kv := map[string]string{}
	s := bufio.NewScanner(r)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return kv, fmt.Errorf("line %d: expected key = \"value\"", n)
		}
		val = strings.TrimSpace(val)
		if v, err := strconv.Unquote(val); err == nil {
			val = v
		}
		kv[strings.TrimSpace(key)] = val
	}
	return kv, s.Err()
}

// Save writes the config file.
func Save(c Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# kobosync configuration\n\n")
	b.WriteString("# Folder with your EPUB library\n")
	fmt.Fprintf(&b, "library = %s\n\n", strconv.Quote(c.Library))
	b.WriteString("# Folder on the Kobo that synced books are written to\n")
	fmt.Fprintf(&b, "kobo_dir = %s\n\n", strconv.Quote(c.KoboDir))
	b.WriteString("# Fixed Kobo mount point; leave empty to detect it automatically\n")
	fmt.Fprintf(&b, "mount = %s\n\n", strconv.Quote(c.Mount))
	b.WriteString("# Interface language (en, de, or any label file in " + filepath.Join(Dir(), "locales") + ")\n")
	fmt.Fprintf(&b, "language = %s\n", strconv.Quote(c.Language))
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

// ExpandHome replaces a leading ~ with the home directory.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
