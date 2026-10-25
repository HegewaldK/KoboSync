// Package epubtest writes small EPUB files for tests.
package epubtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// Book describes a test EPUB.
type Book struct {
	Title, Author, Language, Publisher string
	Identifier                         string // e.g. "urn:isbn:978…" or "urn:uuid:…"
	Series, SeriesIndex                string // written as Calibre metadata
	EPUB3                              bool
	Cover                              color.Color // nil for no cover
}

// Write creates an EPUB at path.
func Write(path string, b Book) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	mt, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	mt.Write([]byte("application/epub+zip"))
	add := func(name, content string) {
		w, _ := zw.Create(name)
		w.Write([]byte(content))
	}
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)

	version := "2.0"
	if b.EPUB3 {
		version = "3.0"
	}
	var meta, items strings.Builder
	fmt.Fprintf(&meta, "<dc:title>%s</dc:title>\n", b.Title)
	if b.EPUB3 {
		fmt.Fprintf(&meta, `<dc:creator id="c1">%s</dc:creator><meta refines="#c1" property="role">aut</meta>`+"\n", b.Author)
	} else {
		fmt.Fprintf(&meta, `<dc:creator opf:role="aut">%s</dc:creator>`+"\n", b.Author)
	}
	fmt.Fprintf(&meta, "<dc:language>%s</dc:language>\n<dc:publisher>%s</dc:publisher>\n", b.Language, b.Publisher)
	fmt.Fprintf(&meta, `<dc:identifier id="bookid">%s</dc:identifier>`+"\n", b.Identifier)
	fmt.Fprintf(&meta, "<dc:description>&lt;p&gt;A test book called %s.&lt;/p&gt;</dc:description>\n", b.Title)
	if b.Series != "" {
		fmt.Fprintf(&meta, `<meta name="calibre:series" content="%s"/><meta name="calibre:series_index" content="%s"/>`+"\n", b.Series, b.SeriesIndex)
	}
	items.WriteString(`<item id="ch1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>` + "\n")
	if b.Cover != nil {
		if b.EPUB3 {
			items.WriteString(`<item id="img" href="images/front%20cover.png" media-type="image/png" properties="cover-image"/>` + "\n")
		} else {
			meta.WriteString(`<meta name="cover" content="img"/>` + "\n")
			items.WriteString(`<item id="img" href="images/front%20cover.png" media-type="image/png"/>` + "\n")
		}
	}
	add("OEBPS/content.opf", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:opf="http://www.idpf.org/2007/opf" version="%s" unique-identifier="bookid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
%s</metadata>
<manifest>
%s</manifest>
<spine><itemref idref="ch1"/></spine>
</package>`, version, meta.String(), items.String()))
	add("OEBPS/text/ch1.xhtml", fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>%s</title></head>
<body><h1>Chapter 1</h1><p>It was a dark and stormy night. The end.</p></body></html>`, b.Title))
	if b.Cover != nil {
		img := image.NewRGBA(image.Rect(0, 0, 60, 90))
		for y := 0; y < 90; y++ {
			for x := 0; x < 60; x++ {
				img.Set(x, y, b.Cover)
				if y > 30 && y < 45 {
					img.Set(x, y, color.White) // a band so the cover is not flat
				}
			}
		}
		w, _ := zw.Create("OEBPS/images/front cover.png")
		png.Encode(w, img)
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// WriteCalibre creates a Calibre book folder: metadata.opf, cover.jpg and the
// given files, which are minimal PDFs (.pdf) or EPUBs (.epub).
func WriteCalibre(dir string, b Book, files ...string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var ids strings.Builder
	fmt.Fprintf(&ids, `<dc:identifier opf:scheme="uuid" id="uuid_id">%s</dc:identifier>`, b.Identifier)
	opf := fmt.Sprintf(`<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="uuid_id" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier opf:scheme="calibre" id="calibre_id">12</dc:identifier>
    %s
    <dc:title>%s</dc:title>
    <dc:creator opf:role="aut">%s</dc:creator>
    <dc:publisher>%s</dc:publisher>
    <dc:language>%s</dc:language>
    <meta name="calibre:timestamp" content="2026-01-01T00:00:00+00:00"/>
  </metadata>
  <guide><reference type="cover" title="Cover" href="cover.jpg"/></guide>
</package>`, ids.String(), b.Title, b.Author, b.Publisher, b.Language)
	if err := os.WriteFile(filepath.Join(dir, "metadata.opf"), []byte(opf), 0o644); err != nil {
		return err
	}
	if b.Cover != nil {
		img := image.NewRGBA(image.Rect(0, 0, 60, 90))
		for y := 0; y < 90; y++ {
			for x := 0; x < 60; x++ {
				img.Set(x, y, b.Cover)
			}
		}
		f, err := os.Create(filepath.Join(dir, "cover.jpg"))
		if err != nil {
			return err
		}
		jpeg.Encode(f, img, nil)
		f.Close()
	}
	for _, name := range files {
		p := filepath.Join(dir, name)
		var err error
		if strings.HasSuffix(name, ".pdf") {
			err = WritePDF(p, b.Title, b.Author)
		} else {
			err = Write(p, b)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// WritePDF creates a minimal PDF with an info dictionary.
func WritePDF(path, title, author string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	pdf := "%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\n" +
		"3 0 obj\n<< /Title (" + title + ") /Author (" + author + ") >>\nendobj\n" +
		"trailer\n<< /Root 1 0 R /Info 3 0 R >>\n%%EOF\n"
	return os.WriteFile(path, []byte(pdf), 0o644)
}
