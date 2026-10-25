package library

import (
	"path/filepath"
	"strings"
	"testing"

	"kobosync/internal/epub/epubtest"
)

// Crafted books must not get terminal escape sequences onto the screen. EPUB
// metadata is already safe (Go's XML parser rejects control characters), but
// PDF metadata and file names are not.
func TestScanStripsControlCharacters(t *testing.T) {
	dir := t.TempDir()
	if err := epubtest.WritePDF(filepath.Join(dir, "evil.pdf"), "Evil\033]52;c;aGFjaw==\007Book", "Mal\033[2JLory"); err != nil {
		t.Fatal(err)
	}
	if err := epubtest.WritePDF(filepath.Join(dir, "name\x1b]0;pwned\x07.pdf"), "", ""); err != nil {
		t.Fatal(err)
	}
	books, err := Scan(dir, OpenCache(""))
	if err != nil || len(books) != 2 {
		t.Fatalf("scan: %v, %d books", err, len(books))
	}
	for _, b := range books {
		for _, s := range []string{b.Title(), b.Author()} {
			if strings.ContainsAny(s, "\x1b\x07") {
				t.Errorf("control character left in %q", s)
			}
		}
	}
	titles := books[0].Title() + " | " + books[1].Title()
	if !strings.Contains(titles, "Evil]52;c;aGFjaw==Book") || !strings.Contains(titles, "name]0;pwned") {
		t.Errorf("titles %q", titles)
	}
}
