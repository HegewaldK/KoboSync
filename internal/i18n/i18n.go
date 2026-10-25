// Package i18n loads the interface labels from label files.
//
// English and German are built in. Files named <code>.toml in the user's
// locales folder add languages or override single labels of a built-in one;
// labels missing in a language fall back to English.
package i18n

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"kobosync/internal/config"
)

//go:embed locales/*.toml
var builtin embed.FS

// Fallback is the language used for missing labels.
const Fallback = "en"

// Locale is one interface language.
type Locale struct {
	Code     string
	labels   map[string]string
	fallback *Locale
}

// Set holds all available languages.
type Set struct {
	locales map[string]*Locale
	codes   []string
}

// Load reads the built-in label files and those in dir. Broken user files
// are skipped and reported in the returned errors.
func Load(dir string) (*Set, []error) {
	s := &Set{locales: map[string]*Locale{}}
	var errs []error
	entries, _ := fs.ReadDir(builtin, "locales")
	for _, e := range entries {
		f, _ := builtin.Open("locales/" + e.Name())
		kv, err := config.ParseKV(f)
		f.Close()
		if err != nil {
			panic(fmt.Sprintf("built-in label file %s: %v", e.Name(), err))
		}
		s.merge(strings.TrimSuffix(e.Name(), ".toml"), kv)
	}
	if dir != "" {
		files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
		for _, p := range files {
			f, err := os.Open(p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			kv, err := config.ParseKV(f)
			f.Close()
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", p, err))
				continue
			}
			s.merge(strings.TrimSuffix(filepath.Base(p), ".toml"), kv)
		}
	}
	en := s.locales[Fallback]
	for _, l := range s.locales {
		if l != en {
			l.fallback = en
		}
		s.codes = append(s.codes, l.Code)
	}
	sort.Strings(s.codes)
	return s, errs
}

func (s *Set) merge(code string, kv map[string]string) {
	l, ok := s.locales[code]
	if !ok {
		l = &Locale{Code: code, labels: map[string]string{}}
		s.locales[code] = l
	}
	for k, v := range kv {
		l.labels[k] = v
	}
}

// Get returns the language with the given code, or English.
func (s *Set) Get(code string) *Locale {
	if l, ok := s.locales[code]; ok {
		return l
	}
	return s.locales[Fallback]
}

// Next returns the language after code, for cycling through them.
func (s *Set) Next(code string) *Locale {
	for i, c := range s.codes {
		if c == code {
			return s.locales[s.codes[(i+1)%len(s.codes)]]
		}
	}
	return s.locales[s.codes[0]]
}

// T returns the label for key; unknown keys are returned as they are.
func (l *Locale) T(key string) string {
	for x := l; x != nil; x = x.fallback {
		if v, ok := x.labels[key]; ok {
			return v
		}
	}
	return key
}

// F formats the label for key with fmt.Sprintf.
func (l *Locale) F(key string, args ...any) string {
	return fmt.Sprintf(l.T(key), args...)
}

// Name is the language's own name, e.g. "Deutsch".
func (l *Locale) Name() string { return l.T("name") }

// Date formats t with the language's date_format. Tokens: YYYY, MMM (short
// month name from "months"), MM, M, DD, D, HH, H, hh, h, mm, A (AM/PM).
func (l *Locale) Date(t time.Time) string {
	format := l.T("date_format")
	months := strings.Split(l.T("months"), ",")
	tokens := []struct {
		tok string
		val func() string
	}{
		{"YYYY", func() string { return strconv.Itoa(t.Year()) }},
		{"MMM", func() string {
			if len(months) == 12 {
				return strings.TrimSpace(months[t.Month()-1])
			}
			return t.Month().String()[:3]
		}},
		{"MM", func() string { return fmt.Sprintf("%02d", int(t.Month())) }},
		{"M", func() string { return strconv.Itoa(int(t.Month())) }},
		{"DD", func() string { return fmt.Sprintf("%02d", t.Day()) }},
		{"D", func() string { return strconv.Itoa(t.Day()) }},
		{"HH", func() string { return fmt.Sprintf("%02d", t.Hour()) }},
		{"H", func() string { return strconv.Itoa(t.Hour()) }},
		{"hh", func() string { return fmt.Sprintf("%02d", (t.Hour()+11)%12+1) }},
		{"h", func() string { return strconv.Itoa((t.Hour()+11)%12 + 1) }},
		{"mm", func() string { return fmt.Sprintf("%02d", t.Minute()) }},
		{"A", func() string {
			if t.Hour() < 12 {
				return "AM"
			}
			return "PM"
		}},
	}
	var b strings.Builder
outer:
	for i := 0; i < len(format); {
		for _, tk := range tokens {
			if strings.HasPrefix(format[i:], tk.tok) {
				b.WriteString(tk.val())
				i += len(tk.tok)
				continue outer
			}
		}
		b.WriteByte(format[i])
		i++
	}
	return b.String()
}

// Bytes formats a size like "1.5 MB" with the language's decimal separator.
func (l *Locale) Bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	s := fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
	return strings.Replace(s, ".", l.T("decimal"), 1)
}

// LanguageName returns the name of a book language code, e.g. "German" for
// "de" or "ger", or the code itself when it is unknown.
func (l *Locale) LanguageName(code string) string {
	c := LangCode(code)
	if c == "" {
		return ""
	}
	if v := l.T("lang." + c); v != "lang."+c {
		return v
	}
	return code
}

// threeLetter maps ISO 639-2 codes that EPUBs and Calibre use to ISO 639-1.
var threeLetter = map[string]string{
	"ger": "de", "deu": "de", "eng": "en", "fre": "fr", "fra": "fr", "spa": "es",
	"ita": "it", "dut": "nl", "nld": "nl", "por": "pt", "swe": "sv", "dan": "da",
	"nor": "no", "nob": "no", "fin": "fi", "pol": "pl", "cze": "cs", "ces": "cs",
	"rus": "ru", "jpn": "ja", "chi": "zh", "zho": "zh", "tur": "tr", "gre": "el", "ell": "el",
	"lat": "la", "hun": "hu",
}

// LangCode normalises a language tag to a short lower case code: "en-US" → "en".
func LangCode(tag string) string {
	c, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	c, _, _ = strings.Cut(c, "_")
	if two, ok := threeLetter[c]; ok {
		return two
	}
	return c
}
