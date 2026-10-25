// Package syncer matches library books with books on the Kobo and applies
// additions, updates and removals.
package syncer

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	"github.com/pgaskin/kepubify/v4/kepub"

	"kobosync/internal/kobo"
	"kobosync/internal/library"
)

// Entry pairs a library book with its copy on the Kobo. Either side may be nil.
type Entry struct {
	Lib      *library.Book
	Kobo     *library.Book
	Progress *kobo.Progress
	Outdated bool // the library file changed since it was synced
	Want     bool // the user wants this book on the Kobo
}

// Key identifies the entry across rescans.
func (e *Entry) Key() string {
	if e.Lib != nil {
		return "lib:" + e.Lib.Path
	}
	return "kobo:" + e.Kobo.Path
}

// Book returns the side that carries the metadata to display.
func (e *Entry) Book() *library.Book {
	if e.Lib != nil {
		return e.Lib
	}
	return e.Kobo
}

// Action is what a sync would do with an entry.
type Action int

const (
	None Action = iota
	Add
	Update
	Remove
)

func (e *Entry) Action() Action {
	switch {
	case e.Want && e.Kobo == nil && e.Lib != nil:
		return Add
	case !e.Want && e.Kobo != nil:
		return Remove
	case e.Want && e.Kobo != nil && e.Outdated:
		return Update
	}
	return None
}

// Match pairs library and Kobo books. A book synced by kobosync is matched via
// the manifest; everything else by ISBN, identifier or title and author.
func Match(lib, dev []library.Book, manifest kobo.Manifest, progress map[string]kobo.Progress) []Entry {
	bySource := map[string]int{}
	byKey := map[string]int{}
	for i := range dev {
		if me, ok := manifest[dev[i].Rel]; ok {
			bySource[me.Source] = i
		}
		for _, k := range keys(&dev[i]) {
			if _, dup := byKey[k]; !dup {
				byKey[k] = i
			}
		}
	}

	used := make([]bool, len(dev))
	entries := make([]Entry, 0, len(lib)+len(dev))
	for i := range lib {
		e := Entry{Lib: &lib[i]}
		j, ok := bySource[lib[i].Rel]
		if !ok || used[j] {
			ok = false
			for _, k := range keys(&lib[i]) {
				if j, ok = byKey[k]; ok && !used[j] {
					break
				}
				ok = false
			}
		}
		if ok {
			used[j] = true
			e.Kobo = &dev[j]
			if me, found := manifest[dev[j].Rel]; found && me.Source == lib[i].Rel {
				e.Outdated = me.Size != lib[i].Size || me.ModTime != lib[i].ModTime.Unix()
			}
		}
		entries = append(entries, e)
	}
	for j := range dev {
		if !used[j] {
			entries = append(entries, Entry{Kobo: &dev[j]})
		}
	}
	for i := range entries {
		e := &entries[i]
		e.Want = e.Kobo != nil
		if e.Kobo != nil {
			if p, ok := progress[e.Kobo.Rel]; ok {
				e.Progress = &p
			}
		}
	}
	return entries
}

func keys(b *library.Book) []string {
	var k []string
	if b.Meta.ISBN != "" {
		k = append(k, "isbn:"+b.Meta.ISBN)
	}
	for _, id := range b.Meta.IDs {
		k = append(k, "id:"+id)
	}
	if t := norm(b.Meta.Title); t != "" {
		k = append(k, "ta:"+t+"|"+norm(b.Author()))
	}
	return k
}

// norm lowercases and drops everything but letters and digits.
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Event reports progress while a sync runs.
type Event struct {
	Done, Total int
	Entry       *Entry
	Action      Action
	Err         error
}

// Apply performs the pending action of every entry. Books are written to
// koboDir (relative to the Kobo root), mirroring the library folder layout.
func Apply(ctx context.Context, dev *kobo.Device, koboDir string, entries []*Entry, report func(Event)) error {
	manifest := dev.LoadManifest()
	conv := kepub.NewConverter()
	for i, e := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		act := e.Action()
		var err error
		switch act {
		case Add:
			err = add(ctx, conv, dev, koboDir, e, manifest)
		case Update:
			err = write(ctx, conv, e.Lib.Path, e.Kobo.Path)
			if err == nil {
				manifest[e.Kobo.Rel] = kobo.ManifestEntry{Source: e.Lib.Rel, Size: e.Lib.Size, ModTime: e.Lib.ModTime.Unix()}
			}
		case Remove:
			err = remove(dev.Mount, e.Kobo.Path)
			if err == nil {
				delete(manifest, e.Kobo.Rel)
			}
		}
		if act != None {
			if serr := dev.SaveManifest(manifest); err == nil {
				err = serr
			}
		}
		report(Event{Done: i + 1, Total: len(entries), Entry: e, Action: act, Err: err})
	}
	syscall.Sync()
	return nil
}

// TargetExt is the extension a library file gets on the Kobo: EPUBs are
// converted to KEPUB, PDFs are copied as they are.
func TargetExt(b *library.Book) string {
	if b.Kind() == library.PDF {
		return ".pdf"
	}
	return ".kepub.epub"
}

func add(ctx context.Context, conv *kepub.Converter, dev *kobo.Device, koboDir string, e *Entry, manifest kobo.Manifest) error {
	rel := strings.TrimSuffix(e.Lib.Rel, path.Ext(e.Lib.Rel))
	rel = strings.TrimSuffix(rel, ".kepub")
	rel = filepath.FromSlash(path.Join(koboDir, rel))
	ext := TargetExt(e.Lib)
	dst := filepath.Join(dev.Mount, rel+ext)
	// kobo_dir comes from the config; never write outside the Kobo.
	if r, err := filepath.Rel(dev.Mount, dst); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return fmt.Errorf("target %s is outside the Kobo", dst)
	}
	for n := 2; ; n++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(dev.Mount, fmt.Sprintf("%s (%d)%s", rel, n, ext))
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := write(ctx, conv, e.Lib.Path, dst); err != nil {
		return err
	}
	koboRel, _ := filepath.Rel(dev.Mount, dst)
	manifest[filepath.ToSlash(koboRel)] = kobo.ManifestEntry{Source: e.Lib.Rel, Size: e.Lib.Size, ModTime: e.Lib.ModTime.Unix()}
	return nil
}

// write converts an EPUB to a KEPUB at dst; KEPUBs and PDFs are copied.
// It writes to a temporary file first so an interrupted sync leaves no broken book.
func write(ctx context.Context, conv *kepub.Converter, src, dst string) error {
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if library.Kind(src) == library.EPUB {
		err = convert(ctx, conv, out, src)
	} else {
		err = copyFile(out, src)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func convert(ctx context.Context, conv *kepub.Converter, w io.Writer, src string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	return conv.Convert(ctx, w, &zr.Reader)
}

func copyFile(w io.Writer, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// remove deletes a book and any folders it leaves empty, up to the Kobo root.
func remove(root, p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	for dir := filepath.Dir(p); dir != root && strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break // not empty
		}
	}
	return nil
}
