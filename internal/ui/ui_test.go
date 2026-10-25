package ui

import (
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"database/sql"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"kobosync/internal/config"
	"kobosync/internal/epub/epubtest"
	"kobosync/internal/i18n"
	"kobosync/internal/library"
)

// drive feeds the results of cmd back into the model until it settles.
// Commands that block (ticks, cursor blinks) are dropped after a short wait.
func drive(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		select {
		case msg := <-ch:
			switch msg := msg.(type) {
			case nil, tickMsg:
			case tea.BatchMsg:
				queue = append(queue, msg...)
			default:
				var next tea.Cmd
				m, next = m.Update(msg)
				queue = append(queue, next)
			}
		case <-time.After(300 * time.Millisecond):
		}
	}
	return m
}

func key(t *testing.T, m tea.Model, k string) tea.Model {
	var msg tea.KeyMsg
	switch k {
	case " ":
		msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	m, cmd := m.Update(msg)
	return drive(t, m, cmd)
}

func TestScreens(t *testing.T) {
	// Keys like L save the config; keep that away from the real one.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	lipgloss.SetColorProfile(termenv.TrueColor)
	root := t.TempDir()
	lib := filepath.Join(root, "Books")
	mount := filepath.Join(root, "KOBOeReader")
	epubtest.Write(filepath.Join(lib, "Rothfuss/Der Name des Windes.epub"), epubtest.Book{Title: "Der Name des Windes", Author: "Patrick Rothfuss",
		Language: "de", Publisher: "Klett-Cotta", Identifier: "9783608938159", Series: "Die Königsmörder-Chronik", SeriesIndex: "1", Cover: color.RGBA{180, 40, 30, 255}})
	epubtest.Write(filepath.Join(lib, "Weir/Project Hail Mary.epub"), epubtest.Book{Title: "Project Hail Mary", Author: "Andy Weir",
		Language: "en-US", Identifier: "urn:isbn:9780593135204", EPUB3: true, Cover: color.RGBA{30, 70, 190, 255}})
	epubtest.Write(filepath.Join(lib, "Herbert/Dune.epub"), epubtest.Book{Title: "Dune", Author: "Frank Herbert", Language: "en", Identifier: "urn:uuid:1"})
	epubtest.Write(filepath.Join(mount, "Andy Weir/Project Hail Mary.kepub.epub"), epubtest.Book{Title: "Project Hail Mary", Author: "Andy Weir",
		Language: "en", Identifier: "urn:isbn:9780593135204", EPUB3: true})
	os.WriteFile(filepath.Join(lib, "broken.epub"), []byte("nope"), 0o644)
	os.MkdirAll(filepath.Join(mount, ".kobo"), 0o755)
	os.WriteFile(filepath.Join(mount, ".kobo/version"), []byte("N0,4.1.15,4.38.21908,4.1.15,4.1.15,00000000-0000-0000-0000-000000000388\n"), 0o644)
	kafka := epubtest.Book{Title: "Die Verwandlung", Author: "Franz Kafka", Language: "ger", Publisher: "Kurt Wolff",
		Identifier: "6f1d2c3a-0000-4000-8000-000000000001", Cover: color.RGBA{90, 160, 60, 255}}
	epubtest.WriteCalibre(filepath.Join(lib, "Franz Kafka/Die Verwandlung (12)"), kafka, "Die Verwandlung - Franz Kafka.pdf")
	epubtest.Write(filepath.Join(mount, "Franz Kafka/Die Verwandlung - Franz Kafka.epub"), kafka)
	db, _ := sql.Open("sqlite", filepath.Join(mount, ".kobo/KoboReader.sqlite"))
	db.Exec(`CREATE TABLE content (ContentID TEXT, ContentType TEXT, ___PercentRead INTEGER, ReadStatus INTEGER, DateLastRead TEXT);
		INSERT INTO content VALUES ('file:///mnt/onboard/Andy Weir/Project Hail Mary.kepub.epub', '6', 42, 1, '2026-10-20T21:15:00Z');
		INSERT INTO content VALUES ('file:///mnt/onboard/Franz Kafka/Die Verwandlung - Franz Kafka.epub', '6', 100, 2, '2026-09-01T10:00:00Z');`)
	db.Close()
	locales, _ := i18n.Load("")

	cfg := config.Default()
	cfg.Library, cfg.Mount = lib, mount
	var m tea.Model = New(cfg, library.OpenCache(""), locales)
	m = drive(t, m, m.Init())
	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: 170, Height: 36} })

	var dump strings.Builder
	shot := func(name string) string {
		v := m.View()
		for i, l := range strings.Split(v, "\n") {
			if w := ansi.StringWidth(l); w > m.(Model).width {
				t.Errorf("%s: line %d is %d cells wide, screen is %d", name, i, w, m.(Model).width)
			}
		}
		if n := strings.Count(v, "\n") + 1; n != m.(Model).height {
			t.Errorf("%s: %d lines, screen has %d", name, n, m.(Model).height)
		}
		dump.WriteString("===== " + name + " =====\n" + v + "\n")
		return ansi.Strip(v)
	}

	s := shot("start")
	for _, want := range []string{"Actions", "Mark all missing", "Library", "Language", "Progress", "Der Name des Winde", "Frank Herbert",
		"Format (Kobo)", "KEPUB", "finished", "42%", "● Kobo", "Details", "✓ on Kobo", "◆ only on Kobo"} {
		if !strings.Contains(s, want) {
			t.Errorf("start screen lacks %q", want)
		}
	}

	if strings.Index(s, "broken") < strings.Index(s, "Patrick Rothfuss") {
		t.Error("book without author should be last")
	}
	m = key(t, m, "down")
	m = key(t, m, "down")
	raw := m.View()
	s = shot("third row selected")
	if !strings.Contains(s, "Klett-Cotta") || !strings.Contains(raw, "\x1b_Ga=T,U=1,f=100") || !strings.Contains(raw, "\U0010EEEE\u0305\u0305") {
		t.Error("cover of Der Name des Windes was not sent as a Kitty image")
	}
	if strings.Contains(s, "Cover:") {
		t.Error("the pixel art switch should stay hidden")
	}
	m = key(t, m, "c")
	raw = m.View()
	shot("pixel art")
	if !strings.Contains(raw, "\x1b_Ga=T,U=1,f=100") || !strings.Contains(m.(Model).status, "pixel art") {
		t.Error("c does not switch to pixel art")
	}
	m = key(t, m, "c")
	m = key(t, m, "a")
	m = key(t, m, "s")
	s = shot("confirm")
	if !strings.Contains(s, "Sync?") || !strings.Contains(s, "add 2") {
		t.Errorf("confirm bar missing: %q", lastLine(s))
	}
	m = key(t, m, "n")
	m = key(t, m, "/")
	for _, r := range "dune" {
		m = key(t, m, string(r))
	}
	s = shot("filter")
	if strings.Contains(s, "Andy Weir") {
		t.Error("filter did not hide other books")
	}

	m = key(t, m, "esc")
	m = key(t, m, "L")
	for range 3 {
		m = key(t, m, "down") // to Project Hail Mary, which has a last read date
	}
	s = shot("german")
	for _, want := range []string{"Aktionen", "Alle fehlenden", "Bibliothek", "Sprache", "Fortschritt", "gelesen", "Auf dem Kobo", "nur auf dem Kobo", "20.10.2026, "} {
		if !strings.Contains(s, want) {
			t.Errorf("german screen lacks %q", want)
		}
	}

	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: 100, Height: 24} })
	shot("medium, no actions panel")
	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 20} })
	shot("narrow")

	cfg.Library = ""
	m = New(cfg, library.OpenCache(""), locales)
	m = drive(t, m, func() tea.Msg { return tea.WindowSizeMsg{Width: 100, Height: 24} })
	s = shot("choose library")
	if !strings.Contains(s, "Which folder holds your library?") {
		t.Error("library prompt missing")
	}

	if out := os.Getenv("KOBOSYNC_SCREENS"); out != "" {
		os.WriteFile(out, []byte(dump.String()), 0o644)
	}
}

func lastLine(s string) string {
	l := strings.Split(s, "\n")
	return l[len(l)-2]
}

// After an eject or a failed mount the same device is not mounted again
// until it has been unplugged.
func TestNoRemountLoop(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	locales, _ := i18n.Load("")
	cfg := config.Default()
	cfg.Library = t.TempDir()
	var m tea.Model = New(cfg, library.OpenCache(""), locales)

	m, _ = m.Update(detectMsg{unmounted: "/dev/sdz", mountErr: errors.New("Not authorized")})
	if m.(Model).noMount != "/dev/sdz" || !strings.Contains(m.(Model).status, "Not authorized") {
		t.Errorf("failed mount: noMount %q, status %q", m.(Model).noMount, m.(Model).status)
	}
	m, _ = m.Update(detectMsg{unmounted: "/dev/sdz"}) // still plugged in, skipped
	if m.(Model).noMount != "/dev/sdz" {
		t.Error("skip was reset while the Kobo is still plugged in")
	}
	m, _ = m.Update(detectMsg{}) // unplugged
	if m.(Model).noMount != "" {
		t.Error("skip not reset after unplugging")
	}
	m, _ = m.Update(ejectMsg{dev: "/dev/sdz"})
	if m.(Model).noMount != "/dev/sdz" {
		t.Error("ejected Kobo would be mounted right back")
	}
}
