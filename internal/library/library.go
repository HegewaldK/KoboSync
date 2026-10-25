// Package library scans folders for books (EPUB, KEPUB, PDF) and caches their
// metadata. Calibre library folders are recognised: the metadata.opf and
// cover.jpg Calibre keeps next to each book are used, and the formats of one
// book are grouped into a single entry.
package library

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"kobosync/internal/epub"
	"kobosync/internal/pdf"
	"kobosync/internal/safe"
)

// Book is one book on disk.
type Book struct {
	Path      string // absolute path of the file that is synced
	Rel       string // Path relative to the scanned root, with forward slashes
	Size      int64
	ModTime   time.Time
	Meta      epub.Metadata
	Err       string   // set when the metadata could not be read
	Formats   []string // every file of this book, Path first (Calibre keeps one per format)
	CoverFile string   // separate cover image, e.g. Calibre's cover.jpg
}

// Kinds of book files.
const (
	KEPUB = "KEPUB"
	EPUB  = "EPUB"
	PDF   = "PDF"
)

// Kind returns KEPUB, EPUB or PDF for a supported file name, else "".
func Kind(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".kepub.epub"):
		return KEPUB
	case strings.HasSuffix(n, ".epub"):
		return EPUB
	case strings.HasSuffix(n, ".pdf"):
		return PDF
	}
	return ""
}

// Kind is the format of the synced file.
func (b *Book) Kind() string { return Kind(b.Path) }

// Format is the format with its version, e.g. "EPUB 3.0".
func (b *Book) Format() string {
	if b.Meta.Version != "" && b.Kind() != PDF {
		return b.Kind() + " " + b.Meta.Version
	}
	return b.Kind()
}

// AllFormats lists the formats of all files of the book, e.g. "EPUB, PDF".
func (b *Book) AllFormats() string {
	var kinds []string
	for _, f := range b.Formats {
		kinds = append(kinds, Kind(f))
	}
	return strings.Join(kinds, ", ")
}

// Title falls back to the file name when the EPUB has no title.
func (b *Book) Title() string {
	if b.Meta.Title != "" {
		return b.Meta.Title
	}
	name := filepath.Base(b.Path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return safe.Text(strings.ReplaceAll(strings.TrimSuffix(name, ".kepub"), "_", " "))
}

// Author shows the authors as "First Last", also when the book stores them
// as "Last, First". Several authors are joined with " & ".
func (b *Book) Author() string {
	parts := strings.Split(b.Meta.Author, " & ")
	for i, p := range parts {
		if last, first, ok := strings.Cut(p, ", "); ok && !strings.Contains(first, ",") && first != "" && last != "" {
			parts[i] = first + " " + last
		}
	}
	return strings.Join(parts, " & ")
}

// AuthorSort is a sort key by the first author's last name: "orwell george".
func (b *Book) AuthorSort() string {
	first, _, _ := strings.Cut(b.Author(), " & ")
	fields := strings.Fields(strings.ToLower(first))
	if len(fields) < 2 {
		return strings.Join(fields, " ")
	}
	return fields[len(fields)-1] + " " + strings.Join(fields[:len(fields)-1], " ")
}

// Cache maps file paths to metadata, invalidated by size and mtime.
type Cache struct {
	mu      sync.Mutex
	path    string
	entries map[string]cacheEntry
	dirty   bool
}

type cacheEntry struct {
	Size    int64         `json:"size"`
	ModTime int64         `json:"mtime"`
	Meta    epub.Metadata `json:"meta"`
}

// cacheVersion is bumped whenever the parser output changes.
const cacheVersion = 4

// OpenCache loads the cache file at path; a missing or stale file yields an empty cache.
func OpenCache(path string) *Cache {
	c := &Cache{path: path, entries: map[string]cacheEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var f struct {
		Version int                   `json:"version"`
		Entries map[string]cacheEntry `json:"entries"`
	}
	if json.Unmarshal(data, &f) == nil && f.Version == cacheVersion && f.Entries != nil {
		c.entries = f.Entries
	}
	return c
}

// Save writes the cache back to disk if it changed.
func (c *Cache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path == "" {
		return nil
	}
	data, err := json.Marshal(map[string]any{"version": cacheVersion, "entries": c.entries})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	c.dirty = false
	return os.Rename(tmp, c.path)
}

func (c *Cache) get(path string, size int64, mtime time.Time) (epub.Metadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	if !ok || e.Size != size || e.ModTime != mtime.Unix() {
		return epub.Metadata{}, false
	}
	return e.Meta, true
}

func (c *Cache) put(path string, size int64, mtime time.Time, m epub.Metadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[path] = cacheEntry{Size: size, ModTime: mtime.Unix(), Meta: m}
	c.dirty = true
}

// formatOrder is the preference for the file that gets synced when a Calibre
// book has several formats: the Kobo reads KEPUB best, then EPUB, then PDF.
var formatOrder = map[string]int{KEPUB: 0, EPUB: 1, PDF: 2}

type file struct {
	path string
	info fs.FileInfo
}

// Scan walks root and returns every book below it. Hidden directories (like
// the Kobo's .kobo folder with store purchases) are skipped.
func Scan(root string, cache *Cache) ([]Book, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var dirs []string
	files := map[string][]file{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // unreadable subfolder: skip it
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || Kind(name) == "" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		dir := filepath.Dir(p)
		if _, seen := files[dir]; !seen {
			dirs = append(dirs, dir)
		}
		files[dir] = append(files[dir], file{p, info})
		return nil
	})

	var books []Book
	for _, dir := range dirs {
		group := files[dir]
		opf := filepath.Join(dir, "metadata.opf")
		if st, err := os.Stat(opf); err == nil && !st.IsDir() {
			books = append(books, calibreBook(root, dir, opf, st, group, cache))
			continue
		}
		for _, f := range group {
			b := newBook(root, f)
			if m, ok := cache.get(f.path, b.Size, b.ModTime); ok {
				b.Meta = m
			} else if m, err := readMeta(f.path); err == nil {
				m = cleanMeta(m)
				b.Meta = m
				cache.put(f.path, b.Size, b.ModTime, m)
			} else {
				b.Err = err.Error()
			}
			books = append(books, b)
		}
	}
	sort.Slice(books, func(i, j int) bool { return books[i].Rel < books[j].Rel })
	return books, err
}

func newBook(root string, f file) Book {
	rel, _ := filepath.Rel(root, f.path)
	return Book{Path: f.path, Rel: filepath.ToSlash(rel), Size: f.info.Size(), ModTime: f.info.ModTime(), Formats: []string{f.path}}
}

// calibreBook builds one book from a Calibre book folder.
func calibreBook(root, dir, opf string, opfInfo fs.FileInfo, files []file, cache *Cache) Book {
	sort.SliceStable(files, func(i, j int) bool {
		return formatOrder[Kind(files[i].path)] < formatOrder[Kind(files[j].path)]
	})
	b := newBook(root, files[0])
	for _, f := range files[1:] {
		b.Formats = append(b.Formats, f.path)
	}
	if m, ok := cache.get(opf, opfInfo.Size(), opfInfo.ModTime()); ok {
		b.Meta = m
	} else if m, err := epub.ReadOPF(opf); err == nil {
		m = cleanMeta(m)
		b.Meta = m
		cache.put(opf, opfInfo.Size(), opfInfo.ModTime(), m)
	} else {
		b.Err = err.Error()
	}
	if cover := filepath.Join(dir, "cover.jpg"); fileExists(cover) {
		b.CoverFile = cover
	}
	return b
}

// cleanMeta strips control characters from metadata, which comes from
// untrusted files and ends up on the terminal.
func cleanMeta(m epub.Metadata) epub.Metadata {
	for _, f := range []*string{&m.Title, &m.Author, &m.Publisher, &m.Language, &m.ISBN,
		&m.Identifier, &m.Series, &m.SeriesIndex, &m.Version} {
		*f = safe.Text(*f)
	}
	m.Description = safe.Multiline(m.Description)
	for i := range m.IDs {
		m.IDs[i] = safe.Text(m.IDs[i])
	}
	return m
}

func readMeta(p string) (epub.Metadata, error) {
	if Kind(p) == PDF {
		return pdf.Read(p)
	}
	return epub.Read(p)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
