package epub_test

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"kobosync/internal/epub"
	"kobosync/internal/epub/epubtest"
)

func TestReadEPUB2(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.epub")
	err := epubtest.Write(p, epubtest.Book{
		Title: "Der Name des Windes", Author: "Patrick Rothfuss", Language: "de", Publisher: "Klett-Cotta",
		Identifier: "978-3-608-93815-9", Series: "Die Königsmörder-Chronik", SeriesIndex: "1.0",
		Cover: color.RGBA{200, 30, 30, 255},
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := epub.Read(p)
	if err != nil {
		t.Fatal(err)
	}
	want := epub.Metadata{
		Title: "Der Name des Windes", Author: "Patrick Rothfuss", Publisher: "Klett-Cotta", Language: "de",
		ISBN: "9783608938159", Identifier: "978-3-608-93815-9", IDs: []string{"978-3-608-93815-9"}, Series: "Die Königsmörder-Chronik", SeriesIndex: "1",
		Description: "A test book called Der Name des Windes.", Version: "2.0", Cover: "OEBPS/images/front cover.png",
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got  %+v\nwant %+v", m, want)
	}
	img, err := epub.Cover(p, m)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 60 || b.Dy() != 90 {
		t.Errorf("cover size %v", b)
	}
}

func TestReadEPUB3(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.epub")
	epubtest.Write(p, epubtest.Book{
		Title: "Project Hail Mary", Author: "Andy Weir", Language: "en-US",
		Identifier: "urn:isbn:9780593135204", EPUB3: true, Cover: color.RGBA{20, 60, 200, 255},
	})
	m, err := epub.Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Author != "Andy Weir" || m.ISBN != "9780593135204" || m.Version != "3.0" || m.Cover != "OEBPS/images/front cover.png" {
		t.Errorf("unexpected metadata %+v", m)
	}
}

func TestUUIDIsNoISBN(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.epub")
	epubtest.Write(p, epubtest.Book{Title: "Dune", Author: "Frank Herbert", Identifier: "urn:uuid:9780441013593-aaaa"})
	m, _ := epub.Read(p)
	if m.ISBN != "" || m.Cover != "" {
		t.Errorf("ISBN %q, cover %q; want both empty", m.ISBN, m.Cover)
	}
}

func TestBroken(t *testing.T) {
	p := filepath.Join(t.TempDir(), "broken.epub")
	os.WriteFile(p, []byte("not a zip"), 0o644)
	if _, err := epub.Read(p); err == nil {
		t.Error("expected an error")
	}
}

// A cover that claims a huge size in its header is rejected before decoding.
func TestCoverTooLarge(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	data := buf.Bytes()
	// IHDR data starts at byte 16: width and height, then the chunk CRC at 29.
	binary.BigEndian.PutUint32(data[16:], 100000)
	binary.BigEndian.PutUint32(data[20:], 100000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	p := filepath.Join(t.TempDir(), "cover.png")
	os.WriteFile(p, data, 0o644)
	if _, err := epub.CoverFile(p); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected a size error, got %v", err)
	}
}
