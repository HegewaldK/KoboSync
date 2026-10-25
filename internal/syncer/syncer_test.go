package syncer_test

import (
	"archive/zip"
	"context"
	"database/sql"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"kobosync/internal/epub/epubtest"
	"kobosync/internal/kobo"
	"kobosync/internal/library"
	"kobosync/internal/syncer"
)

var (
	wind = epubtest.Book{Title: "Der Name des Windes", Author: "Patrick Rothfuss", Language: "de",
		Identifier: "9783608938159", Series: "Die Königsmörder-Chronik", SeriesIndex: "1", Cover: color.RGBA{200, 30, 30, 255}}
	hailMary = epubtest.Book{Title: "Project Hail Mary", Author: "Andy Weir", Language: "en",
		Identifier: "urn:isbn:9780593135204", EPUB3: true, Cover: color.RGBA{20, 60, 200, 255}}
	dune  = epubtest.Book{Title: "Dune", Author: "Frank Herbert", Language: "en", Identifier: "urn:uuid:1111"}
	other = epubtest.Book{Title: "Only On Kobo", Author: "Someone Else", Language: "en", Identifier: "urn:uuid:2222"}
)

// setup creates a library with three books and a fake Kobo that already holds
// Project Hail Mary (under another file name, as Calibre would copy it), a
// book that is not in the library, and a reading progress database.
func setup(t *testing.T) (libDir string, dev *kobo.Device) {
	t.Helper()
	root := t.TempDir()
	libDir = filepath.Join(root, "library")
	mount := filepath.Join(root, "KOBOeReader")
	must(t, epubtest.Write(filepath.Join(libDir, "Rothfuss/Der Name des Windes.epub"), wind))
	must(t, epubtest.Write(filepath.Join(libDir, "Weir - Project Hail Mary.epub"), hailMary))
	must(t, epubtest.Write(filepath.Join(libDir, "Herbert/Dune.epub"), dune))

	must(t, epubtest.Write(filepath.Join(mount, "Andy Weir/Project Hail Mary - Andy Weir.kepub.epub"), hailMary))
	must(t, epubtest.Write(filepath.Join(mount, "misc/Only On Kobo.epub"), other))
	must(t, epubtest.Write(filepath.Join(mount, ".kobo/kepub/store-book"), other)) // store purchase: must be ignored
	must(t, os.WriteFile(filepath.Join(mount, ".kobo/version"), []byte("N000000000000,4.1.15,4.38.21908,4.1.15,4.1.15,00000000-0000-0000-0000-000000000388\n"), 0o644))

	db, err := sql.Open("sqlite", filepath.Join(mount, ".kobo/KoboReader.sqlite"))
	must(t, err)
	_, err = db.Exec(`CREATE TABLE content (ContentID TEXT, ContentType TEXT, ___PercentRead INTEGER, ReadStatus INTEGER, DateLastRead TEXT);
		INSERT INTO content VALUES ('file:///mnt/onboard/Andy Weir/Project Hail Mary - Andy Weir.kepub.epub', '6', 42, 1, '2026-10-20T21:15:00Z');
		INSERT INTO content VALUES ('file:///mnt/onboard/Andy Weir/Project Hail Mary - Andy Weir.kepub.epub!!ch1', '9', 0, 0, NULL);
		INSERT INTO content VALUES ('file:///mnt/onboard/misc/Only On Kobo.epub', '6', 100, 2, '2026-09-01T10:00:00.000');`)
	must(t, err)
	db.Close()

	dev = kobo.Detect(mount)
	if dev == nil {
		t.Fatal("fake Kobo not detected")
	}
	return libDir, dev
}

func scan(t *testing.T, libDir string, dev *kobo.Device) []syncer.Entry {
	t.Helper()
	cache := library.OpenCache("")
	lib, err := library.Scan(libDir, cache)
	must(t, err)
	books, err := library.Scan(dev.Mount, cache)
	must(t, err)
	progress, _ := dev.Progress() // a Kobo without database just has no progress
	return syncer.Match(lib, books, dev.LoadManifest(), progress)
}

func find(entries []syncer.Entry, title string) *syncer.Entry {
	for i := range entries {
		if entries[i].Book().Meta.Title == title {
			return &entries[i]
		}
	}
	return nil
}

func TestMatchAndSync(t *testing.T) {
	libDir, dev := setup(t)
	if dev.Firmware != "4.38.21908" {
		t.Errorf("firmware %q", dev.Firmware)
	}

	entries := scan(t, libDir, dev)
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(entries))
	}
	hm := find(entries, "Project Hail Mary")
	if hm.Lib == nil || hm.Kobo == nil {
		t.Fatalf("Project Hail Mary not matched across renamed files: %+v", hm)
	}
	if hm.Progress == nil || hm.Progress.Percent != 42 || hm.Progress.Status != kobo.Reading {
		t.Errorf("progress %+v", hm.Progress)
	}
	if ok := find(entries, "Only On Kobo"); ok.Lib != nil || ok.Kobo == nil || ok.Progress.Status != kobo.Finished {
		t.Errorf("kobo-only entry %+v", ok)
	}
	if w := find(entries, "Der Name des Windes"); w.Kobo != nil || w.Action() != syncer.None {
		t.Errorf("wind should be missing and untouched: %+v", w)
	}

	// Add two books, remove the Kobo-only one.
	find(entries, "Der Name des Windes").Want = true
	find(entries, "Dune").Want = true
	find(entries, "Only On Kobo").Want = false
	var todo []*syncer.Entry
	for i := range entries {
		if entries[i].Action() != syncer.None {
			todo = append(todo, &entries[i])
		}
	}
	if len(todo) != 3 {
		t.Fatalf("%d pending actions, want 3", len(todo))
	}
	var events []syncer.Event
	must(t, syncer.Apply(context.Background(), dev, "Books", todo, func(e syncer.Event) { events = append(events, e) }))
	for _, e := range events {
		if e.Err != nil {
			t.Errorf("%s: %v", e.Entry.Book().Title(), e.Err)
		}
	}

	windPath := filepath.Join(dev.Mount, "Books/Rothfuss/Der Name des Windes.kepub.epub")
	assertKepub(t, windPath)
	assertKepub(t, filepath.Join(dev.Mount, "Books/Herbert/Dune.kepub.epub"))
	if _, err := os.Stat(filepath.Join(dev.Mount, "misc")); !os.IsNotExist(err) {
		t.Error("removed book's empty folder should be gone")
	}

	// After the sync everything is in place and nothing is pending.
	entries = scan(t, libDir, dev)
	if len(entries) != 3 {
		t.Fatalf("got %d entries after sync, want 3", len(entries))
	}
	for _, e := range entries {
		if e.Kobo == nil || e.Action() != syncer.None {
			t.Errorf("%s: not synced cleanly (kobo=%v action=%v)", e.Book().Title(), e.Kobo != nil, e.Action())
		}
	}

	// Changing a library file marks it outdated, and a sync replaces the copy in place.
	future := time.Now().Add(time.Hour)
	must(t, os.Chtimes(filepath.Join(libDir, "Rothfuss/Der Name des Windes.epub"), future, future))
	entries = scan(t, libDir, dev)
	w := find(entries, "Der Name des Windes")
	if !w.Outdated || w.Action() != syncer.Update {
		t.Fatalf("expected an update, got outdated=%v action=%v", w.Outdated, w.Action())
	}
	must(t, syncer.Apply(context.Background(), dev, "Books", []*syncer.Entry{w}, func(e syncer.Event) { must(t, e.Err) }))
	entries = scan(t, libDir, dev)
	if w := find(entries, "Der Name des Windes"); w.Outdated || w.Kobo.Path != windPath {
		t.Errorf("after update: outdated=%v path=%s", w.Outdated, w.Kobo.Path)
	}
}

// assertKepub checks that path is a converted KEPUB with Kobo spans.
func assertKepub(t *testing.T, path string) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	f, err := zr.Open("OEBPS/text/ch1.xhtml")
	must(t, err)
	data, _ := io.ReadAll(f)
	if !strings.Contains(string(data), "koboSpan") {
		t.Errorf("%s: no koboSpan, not converted", filepath.Base(path))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestCalibreAndPDF(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "Calibre Library")
	mount := filepath.Join(root, "KOBOeReader")
	kafka := epubtest.Book{Title: "Die Verwandlung", Author: "Franz Kafka", Language: "deu", Publisher: "Kurt Wolff",
		Identifier: "6f1d2c3a-0000-4000-8000-000000000001", Cover: color.RGBA{90, 160, 60, 255}}
	faust := epubtest.Book{Title: "Faust", Author: "Johann Wolfgang von Goethe", Language: "deu", Publisher: "Reclam",
		Identifier: "6f1d2c3a-0000-4000-8000-000000000002", Cover: color.RGBA{200, 180, 40, 255}}
	must(t, epubtest.WriteCalibre(filepath.Join(libDir, "Franz Kafka/Die Verwandlung (12)"), kafka, "Die Verwandlung - Franz Kafka.pdf"))
	must(t, epubtest.WriteCalibre(filepath.Join(libDir, "Johann Wolfgang von Goethe/Faust (13)"), faust, "Faust - Johann Wolfgang von Goethe.pdf", "Faust - Johann Wolfgang von Goethe.epub"))
	must(t, epubtest.WritePDF(filepath.Join(libDir, "Papers/whitepaper.pdf"), "Kobo Internals", "A. Hacker"))

	// Calibre converted the PDF to EPUB when it sent it to the Kobo, with a
	// different title spelling but the same Calibre UUID.
	onKobo := kafka
	onKobo.Title = "Verwandlung, Die"
	must(t, epubtest.Write(filepath.Join(mount, "Franz Kafka/Die Verwandlung - Franz Kafka.epub"), onKobo))
	must(t, os.MkdirAll(filepath.Join(mount, ".kobo"), 0o755))
	dev := kobo.Detect(mount)

	entries := scan(t, libDir, dev)
	if len(entries) != 3 {
		for _, e := range entries {
			t.Logf("%s lib=%v kobo=%v", e.Book().Title(), e.Lib != nil, e.Kobo != nil)
		}
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	k := find(entries, "Die Verwandlung")
	if k == nil || k.Kobo == nil || k.Lib.Kind() != library.PDF || k.Kobo.Kind() != library.EPUB {
		t.Fatalf("PDF not matched with its EPUB on the Kobo: %+v", k)
	}
	if k.Lib.Meta.Publisher != "Kurt Wolff" || k.Lib.CoverFile == "" {
		t.Errorf("Calibre metadata or cover not used: %+v", k.Lib)
	}
	f := find(entries, "Faust")
	if f.Lib.Kind() != library.EPUB || f.Lib.AllFormats() != "EPUB, PDF" {
		t.Errorf("Faust: synced kind %s, formats %q", f.Lib.Kind(), f.Lib.AllFormats())
	}
	w := find(entries, "Kobo Internals")
	if w == nil || w.Lib.Meta.Author != "A. Hacker" {
		t.Fatalf("plain PDF metadata not read: %+v", w)
	}

	f.Want, w.Want = true, true
	must(t, syncer.Apply(context.Background(), dev, "Books", []*syncer.Entry{f, w}, func(e syncer.Event) { must(t, e.Err) }))
	assertKepub(t, filepath.Join(mount, "Books/Johann Wolfgang von Goethe/Faust (13)/Faust - Johann Wolfgang von Goethe.kepub.epub"))
	src, _ := os.ReadFile(filepath.Join(libDir, "Papers/whitepaper.pdf"))
	dst, err := os.ReadFile(filepath.Join(mount, "Books/Papers/whitepaper.pdf"))
	if err != nil || string(src) != string(dst) {
		t.Errorf("PDF not copied unchanged: %v", err)
	}
	for _, e := range scan(t, libDir, dev) {
		if e.Kobo == nil || e.Action() != syncer.None {
			t.Errorf("%s not synced cleanly", e.Book().Title())
		}
	}
}

func TestAuthorNames(t *testing.T) {
	for in, want := range map[string][2]string{
		"Orwell, George":                   {"George Orwell", "orwell george"},
		"Jack London":                      {"Jack London", "london jack"},
		"Scott Chacon & Ben Straub":        {"Scott Chacon & Ben Straub", "chacon scott"},
		"Atwood, Margaret & King, Stephen": {"Margaret Atwood & Stephen King", "atwood margaret"},
		"annalara":                         {"annalara", "annalara"},
	} {
		b := library.Book{}
		b.Meta.Author = in
		if got := [2]string{b.Author(), b.AuthorSort()}; got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
