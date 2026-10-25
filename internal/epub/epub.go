// Package epub reads metadata and cover images from EPUB (and KEPUB) files.
package epub

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
)

// Metadata is the subset of OPF metadata kobosync cares about.
type Metadata struct {
	Title       string   `json:"title"`
	Author      string   `json:"author"`
	Publisher   string   `json:"publisher"`
	Language    string   `json:"language"`
	ISBN        string   `json:"isbn"`
	Identifier  string   `json:"identifier"`
	IDs         []string `json:"ids"` // all identifiers, lower case
	Series      string   `json:"series"`
	SeriesIndex string   `json:"series_index"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Cover       string   `json:"cover"` // path of the cover image inside the zip
}

type container struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfPackage struct {
	Version  string `xml:"version,attr"`
	Metadata struct {
		Titles      []string        `xml:"title"`
		Creators    []opfCreator    `xml:"creator"`
		Publishers  []string        `xml:"publisher"`
		Languages   []string        `xml:"language"`
		Identifiers []opfIdentifier `xml:"identifier"`
		Description []string        `xml:"description"`
		Metas       []opfMeta       `xml:"meta"`
	} `xml:"metadata"`
	Items []opfItem `xml:"manifest>item"`
}

type opfCreator struct {
	Name string `xml:",chardata"`
	Role string `xml:"role,attr"`
	ID   string `xml:"id,attr"`
}

type opfIdentifier struct {
	Value  string `xml:",chardata"`
	Scheme string `xml:"scheme,attr"`
}

type opfMeta struct {
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Refines  string `xml:"refines,attr"`
	Value    string `xml:",chardata"`
}

type opfItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

// Read parses the metadata of the EPUB at path.
func Read(p string) (Metadata, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return Metadata{}, err
	}
	defer zr.Close()

	opfPath, err := rootfile(&zr.Reader)
	if err != nil {
		return Metadata{}, err
	}
	var pkg opfPackage
	if err := decodeXML(&zr.Reader, opfPath, &pkg); err != nil {
		return Metadata{}, fmt.Errorf("opf: %w", err)
	}
	return fromOPF(&pkg, path.Dir(opfPath)), nil
}

// Cover decodes the cover image named in m from the EPUB at path.
func Cover(p string, m Metadata) (image.Image, error) {
	if m.Cover == "" {
		return nil, errors.New("no cover")
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return decodeImage(func() (io.ReadCloser, error) { return zr.Open(m.Cover) })
}

// ReadOPF parses a standalone OPF file such as the metadata.opf Calibre keeps
// next to every book in its library.
func ReadOPF(p string) (Metadata, error) {
	f, err := os.Open(p)
	if err != nil {
		return Metadata{}, err
	}
	defer f.Close()
	var pkg opfPackage
	if err := newDecoder(f).Decode(&pkg); err != nil {
		return Metadata{}, fmt.Errorf("opf: %w", err)
	}
	m := fromOPF(&pkg, "")
	m.Version, m.Cover = "", "" // these describe the OPF, not the book files
	return m, nil
}

// CoverFile decodes a cover image stored as a separate file (Calibre's cover.jpg).
func CoverFile(p string) (image.Image, error) {
	return decodeImage(func() (io.ReadCloser, error) { return os.Open(p) })
}

// maxPixels limits cover images; a crafted file can claim a huge size in its
// header and make the decoder allocate gigabytes.
const maxPixels = 40_000_000

// decodeImage checks the image size from its header before decoding it.
func decodeImage(open func() (io.ReadCloser, error)) (image.Image, error) {
	f, err := open()
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("cover too large: %d×%d", cfg.Width, cfg.Height)
	}
	if f, err = open(); err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func rootfile(zr *zip.Reader) (string, error) {
	var c container
	if err := decodeXML(zr, "META-INF/container.xml", &c); err != nil {
		return "", fmt.Errorf("container.xml: %w", err)
	}
	for _, r := range c.Rootfiles {
		if r.FullPath != "" {
			return r.FullPath, nil
		}
	}
	return "", errors.New("no rootfile in container.xml")
}

func decodeXML(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return newDecoder(f).Decode(v)
}

func newDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(io.LimitReader(r, 8<<20))
	d.Strict = false
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return d
}

func fromOPF(pkg *opfPackage, base string) Metadata {
	md := &pkg.Metadata
	m := Metadata{
		Title:     first(md.Titles),
		Publisher: first(md.Publishers),
		Language:  first(md.Languages),
		Version:   pkg.Version,
	}

	// EPUB 3 declares creator roles via <meta refines="#id" property="role">.
	roles := map[string]string{}
	for _, mt := range md.Metas {
		if mt.Property == "role" && strings.HasPrefix(mt.Refines, "#") {
			roles[mt.Refines[1:]] = strings.TrimSpace(mt.Value)
		}
	}
	var authors []string
	for _, c := range md.Creators {
		role := c.Role
		if role == "" {
			role = roles[c.ID]
		}
		if name := clean(c.Name); name != "" && (role == "" || role == "aut") {
			authors = append(authors, name)
		}
	}
	m.Author = strings.Join(authors, " & ")

	for _, id := range md.Identifiers {
		v := clean(id.Value)
		if v == "" {
			continue
		}
		if m.Identifier == "" {
			m.Identifier = v
		}
		m.IDs = append(m.IDs, strings.ToLower(v))
		if m.ISBN == "" {
			m.ISBN = isbn(v, id.Scheme)
		}
	}

	if d := first(md.Description); d != "" {
		m.Description = stripHTML(d)
	}

	// Series: Calibre style <meta name="calibre:series"> or EPUB 3 collections.
	for _, mt := range md.Metas {
		switch mt.Name {
		case "calibre:series":
			m.Series = clean(mt.Content)
		case "calibre:series_index":
			m.SeriesIndex = strings.TrimSuffix(clean(mt.Content), ".0")
		}
	}
	if m.Series == "" {
		for _, mt := range md.Metas {
			if mt.Property == "belongs-to-collection" && m.Series == "" {
				m.Series = clean(mt.Value)
			}
			if mt.Property == "group-position" && m.SeriesIndex == "" {
				m.SeriesIndex = clean(mt.Value)
			}
		}
	}

	m.Cover = coverPath(pkg, base)
	return m
}

func coverPath(pkg *opfPackage, base string) string {
	var href string
	for _, it := range pkg.Items {
		if strings.Contains(" "+it.Properties+" ", " cover-image ") {
			href = it.Href
			break
		}
	}
	if href == "" {
		var coverID string
		for _, mt := range pkg.Metadata.Metas {
			if mt.Name == "cover" {
				coverID = mt.Content
			}
		}
		for _, it := range pkg.Items {
			if coverID != "" && it.ID == coverID {
				href = it.Href
				break
			}
		}
	}
	if href == "" {
		for _, it := range pkg.Items {
			if strings.HasPrefix(it.MediaType, "image/") &&
				(strings.Contains(strings.ToLower(it.ID), "cover") || strings.Contains(strings.ToLower(it.Href), "cover")) {
				href = it.Href
				break
			}
		}
	}
	if href == "" {
		return ""
	}
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	return path.Clean(path.Join(base, href))
}

var nonDigits = regexp.MustCompile(`[^0-9Xx]`)

// isbn extracts a normalized ISBN-10/13 from an identifier, or returns "".
func isbn(v, scheme string) string {
	lower := strings.ToLower(v)
	isISBN := strings.EqualFold(scheme, "isbn") || strings.HasPrefix(lower, "urn:isbn:") || strings.HasPrefix(lower, "isbn")
	if !isISBN && (strings.Contains(lower, "uuid") || strings.Contains(lower, ":")) {
		return ""
	}
	d := strings.ToUpper(nonDigits.ReplaceAllString(v, ""))
	if (len(d) == 13 && (strings.HasPrefix(d, "978") || strings.HasPrefix(d, "979"))) || (len(d) == 10 && isISBN) {
		return d
	}
	return ""
}

var tags = regexp.MustCompile(`(?s)<[^>]*>`)
var spaces = regexp.MustCompile(`[ \t\r\f\v]+`)
var blankLines = regexp.MustCompile(`\n\s*\n+`)

func stripHTML(s string) string {
	s = strings.NewReplacer("</p>", "\n\n", "<br>", "\n", "<br/>", "\n", "<br />", "\n").Replace(s)
	s = html.UnescapeString(tags.ReplaceAllString(s, ""))
	s = spaces.ReplaceAllString(s, " ")
	s = blankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func clean(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

func first(s []string) string {
	for _, v := range s {
		if v = clean(v); v != "" {
			return v
		}
	}
	return ""
}
