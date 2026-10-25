// Package pdf reads title and author from PDF files without a full PDF parser.
//
// It looks at the document information dictionary and, failing that, at the
// XMP metadata packet. Both are usually stored uncompressed. PDFs that keep
// their metadata only in compressed object streams yield empty metadata, and
// callers fall back to the file name.
package pdf

import (
	"bytes"
	"encoding/binary"
	"html"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"kobosync/internal/epub"
)

// headSize and tailSize limit how much of large PDFs is read.
const (
	headSize = 4 << 20
	tailSize = 1 << 20
)

// Read returns the metadata of the PDF at path.
func Read(path string) (epub.Metadata, error) {
	data, err := readEnds(path)
	if err != nil {
		return epub.Metadata{}, err
	}
	if !bytes.HasPrefix(bytes.TrimLeft(data, "\x00\r\n\t "), []byte("%PDF")) {
		return epub.Metadata{}, errNotPDF
	}
	var m epub.Metadata
	info := infoDict(data)
	m.Title = cleanTitle(info["Title"])
	m.Author = info["Author"]
	xmp := xmpMeta(data)
	if m.Title == "" {
		m.Title = cleanTitle(xmp["title"])
	}
	if m.Author == "" {
		m.Author = xmp["creator"]
	}
	m.Language = xmp["language"]
	m.Publisher = xmp["publisher"]
	if id := xmp["identifier"]; id != "" {
		m.Identifier = id
		m.IDs = []string{strings.ToLower(id)}
	}
	return m, nil
}

type pdfError string

func (e pdfError) Error() string { return string(e) }

const errNotPDF = pdfError("not a PDF file")

// readEnds reads the whole file if it is small, else its head and tail, which
// is where the trailer, the info dictionary and the XMP packet normally live.
func readEnds(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() <= headSize+tailSize {
		return io.ReadAll(f)
	}
	buf := make([]byte, headSize+tailSize)
	if _, err := io.ReadFull(f, buf[:headSize]); err != nil {
		return nil, err
	}
	if _, err := f.ReadAt(buf[headSize:], st.Size()-tailSize); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}

var (
	infoRef = regexp.MustCompile(`/Info\s+(\d+)\s+(\d+)\s+R`)
	keyRe   = regexp.MustCompile(`/(Title|Author)\s*`)
)

// infoDict finds the document information dictionary via the last /Info
// reference and returns its Title and Author.
func infoDict(data []byte) map[string]string {
	out := map[string]string{}
	refs := infoRef.FindAllSubmatch(data, -1)
	if len(refs) == 0 {
		return out
	}
	ref := refs[len(refs)-1]
	objRe := regexp.MustCompile(`(?:^|[\s>])` + string(ref[1]) + `\s+` + string(ref[2]) + `\s+obj\b`)
	locs := objRe.FindAllIndex(data, -1)
	if len(locs) == 0 {
		return out
	}
	obj := data[locs[len(locs)-1][1]:]
	if end := bytes.Index(obj, []byte("endobj")); end >= 0 {
		obj = obj[:end]
	}
	for _, loc := range keyRe.FindAllSubmatchIndex(obj, -1) {
		key := string(obj[loc[2]:loc[3]])
		if s, ok := parseString(obj[loc[1]:]); ok {
			if s = strings.TrimSpace(s); s != "" {
				out[key] = s
			}
		}
	}
	return out
}

// parseString decodes a PDF literal "(…)" or hex "<…>" string at the start of b.
func parseString(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	var raw []byte
	switch b[0] {
	case '(':
		depth := 0
		for i := 1; i < len(b); i++ {
			c := b[i]
			switch {
			case c == '\\' && i+1 < len(b):
				i++
				switch e := b[i]; e {
				case 'n':
					raw = append(raw, '\n')
				case 'r':
					raw = append(raw, '\r')
				case 't':
					raw = append(raw, '\t')
				case 'b':
					raw = append(raw, '\b')
				case 'f':
					raw = append(raw, '\f')
				case '\r', '\n':
					// line continuation
				default:
					if e >= '0' && e <= '7' {
						j := i
						for j < len(b) && j < i+3 && b[j] >= '0' && b[j] <= '7' {
							j++
						}
						v, _ := strconv.ParseUint(string(b[i:j]), 8, 8)
						raw = append(raw, byte(v))
						i = j - 1
					} else {
						raw = append(raw, e)
					}
				}
			case c == '(':
				depth++
				raw = append(raw, c)
			case c == ')':
				if depth == 0 {
					return decodeText(raw), true
				}
				depth--
				raw = append(raw, c)
			default:
				raw = append(raw, c)
			}
		}
		return "", false
	case '<':
		end := bytes.IndexByte(b, '>')
		if end < 0 {
			return "", false
		}
		hex := bytes.Map(func(r rune) rune {
			if strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return r
			}
			return -1
		}, b[1:end])
		if len(hex)%2 == 1 {
			hex = append(hex, '0')
		}
		for i := 0; i < len(hex); i += 2 {
			v, _ := strconv.ParseUint(string(hex[i:i+2]), 16, 8)
			raw = append(raw, byte(v))
		}
		return decodeText(raw), true
	}
	return "", false
}

// decodeText handles UTF-16BE (with BOM), UTF-8 (with BOM) and PDFDocEncoding,
// which matches Latin-1 for all printable characters that matter here.
func decodeText(b []byte) string {
	switch {
	case len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, binary.BigEndian.Uint16(b[i:]))
		}
		return string(utf16.Decode(u))
	case len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		return string(b[3:])
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

var (
	xmpRe   = regexp.MustCompile(`(?s)<x:xmpmeta.*?</x:xmpmeta>`)
	xmpTags = map[string]*regexp.Regexp{}
	liRe    = regexp.MustCompile(`(?s)<rdf:li[^>]*>(.*?)</rdf:li>`)
)

func init() {
	for _, tag := range []string{"title", "creator", "language", "publisher", "identifier"} {
		xmpTags[tag] = regexp.MustCompile(`(?s)<dc:` + tag + `[^>]*>(.*?)</dc:` + tag + `>`)
	}
}

// xmpMeta extracts Dublin Core fields from the last XMP packet.
func xmpMeta(data []byte) map[string]string {
	out := map[string]string{}
	packets := xmpRe.FindAll(data, -1)
	if len(packets) == 0 {
		return out
	}
	packet := packets[len(packets)-1]
	for tag, re := range xmpTags {
		m := re.FindSubmatch(packet)
		if m == nil {
			continue
		}
		var vals []string
		for _, li := range liRe.FindAllSubmatch(m[1], -1) {
			if v := strings.TrimSpace(html.UnescapeString(string(li[1]))); v != "" {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			if v := strings.TrimSpace(html.UnescapeString(string(m[1]))); v != "" && !strings.Contains(v, "<") {
				vals = append(vals, v)
			}
		}
		if tag == "title" && len(vals) > 1 {
			vals = vals[:1] // alternative languages of the same title
		}
		sep := ", "
		if tag == "creator" {
			sep = " & "
		}
		out[tag] = strings.Join(vals, sep)
	}
	return out
}

// cleanTitle drops titles that are clearly generated by the authoring tool.
func cleanTitle(t string) string {
	t = strings.TrimSpace(t)
	lower := strings.ToLower(t)
	for _, junk := range []string{"untitled", "microsoft word - ", "microsoft powerpoint - "} {
		if strings.HasPrefix(lower, junk) {
			return ""
		}
	}
	for _, ext := range []string{".doc", ".docx", ".pdf", ".tex", ".dvi", ".odt", ".indd", ".qxd"} {
		if strings.HasSuffix(lower, ext) {
			return ""
		}
	}
	return t
}
