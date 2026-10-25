package i18n

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBuiltinComplete makes sure every built-in language has every English label.
func TestBuiltinComplete(t *testing.T) {
	s, _ := Load("")
	en := s.Get("en")
	for _, code := range s.codes {
		l := s.Get(code)
		for key := range en.labels {
			if _, ok := l.labels[key]; !ok {
				t.Errorf("%s: missing label %q", code, key)
			}
		}
	}
}

func TestFormatting(t *testing.T) {
	s, _ := Load("")
	when := time.Date(2026, 10, 3, 21, 5, 0, 0, time.Local)
	if got := s.Get("de").Date(when); got != "03.10.2026, 21:05" {
		t.Errorf("de date %q", got)
	}
	if got := s.Get("en").Date(when); got != "Oct 3, 2026, 21:05" {
		t.Errorf("en date %q", got)
	}
	if got := s.Get("de").Bytes(1536 * 1024); got != "1,5 MB" {
		t.Errorf("de bytes %q", got)
	}
	if got := s.Get("de").LanguageName("ger"); got != "Deutsch" {
		t.Errorf("language name %q", got)
	}
	if got := s.Get("en").LanguageName("en-US"); got != "English" {
		t.Errorf("language name %q", got)
	}
}

func TestUserFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "de.toml"), []byte(`act.quit = "Tschüss"`+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "fr.toml"), []byte(`name = "Français"`+"\n"+`act.quit = "Quitter"`+"\n"), 0o644)
	s, errs := Load(dir)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if got := s.Get("de").T("act.quit"); got != "Tschüss" {
		t.Errorf("override: %q", got)
	}
	if got := s.Get("de").T("act.eject"); got != "Auswerfen" {
		t.Errorf("override lost other labels: %q", got)
	}
	fr := s.Get("fr")
	if fr.T("act.quit") != "Quitter" || fr.T("act.eject") != "Eject" {
		t.Errorf("fr: %q / %q (fallback to English expected)", fr.T("act.quit"), fr.T("act.eject"))
	}
	if s.Next("de").Code != "en" || s.Next("en").Code != "fr" || s.Next("fr").Code != "de" {
		t.Error("Next does not cycle through de, en, fr")
	}
}
