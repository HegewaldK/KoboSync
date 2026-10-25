package pdf

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInfoDict(t *testing.T) {
	p := write(t, "%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"+
		"7 0 obj\n<< /Title (Die Verwandlung \\(Novelle\\)) /Author <FEFF004600720061006E007A0020004B00610066006B0061> /Producer (x) >>\nendobj\n"+
		"trailer\n<< /Root 1 0 R /Info 7 0 R >>\n%%EOF\n")
	m, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Die Verwandlung (Novelle)" || m.Author != "Franz Kafka" {
		t.Errorf("got %q by %q", m.Title, m.Author)
	}
}

func TestXMPFallback(t *testing.T) {
	p := write(t, "%PDF-1.7\n5 0 obj\n<< /Type /Metadata /Subtype /XML >>\nstream\n"+
		`<x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF><rdf:Description>`+
		`<dc:title><rdf:Alt><rdf:li xml:lang="x-default">Faust &amp; Gretchen</rdf:li></rdf:Alt></dc:title>`+
		`<dc:creator><rdf:Seq><rdf:li>Johann Wolfgang von Goethe</rdf:li></rdf:Seq></dc:creator>`+
		`<dc:language><rdf:Bag><rdf:li>de</rdf:li></rdf:Bag></dc:language>`+
		"</rdf:Description></rdf:RDF></x:xmpmeta>\nendstream\nendobj\n"+
		"6 0 obj\n<< /Title (Microsoft Word - faust.docx) >>\nendobj\ntrailer\n<< /Info 6 0 R >>\n")
	m, err := Read(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Faust & Gretchen" || m.Author != "Johann Wolfgang von Goethe" || m.Language != "de" {
		t.Errorf("got %+v", m)
	}
}

func TestNotPDF(t *testing.T) {
	if _, err := Read(write(t, "hello")); err == nil {
		t.Error("expected an error")
	}
}
