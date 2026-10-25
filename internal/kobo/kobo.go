// Package kobo detects a mounted Kobo eReader and reads its state.
package kobo

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"kobosync/internal/safe"
)

// Device is a mounted Kobo.
type Device struct {
	Mount    string // mount point, e.g. /run/media/me/KOBOeReader
	Dev      string // block device, e.g. /dev/sdb
	Firmware string
}

// Detect looks for a mounted Kobo. extra is an optional mount point from the
// config that is checked first. It returns nil when no Kobo is connected.
func Detect(extra string) *Device {
	var candidates []string
	if extra != "" {
		candidates = append(candidates, extra)
	}
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	for _, base := range []string{"/run/media/" + name, "/media/" + name, "/media", "/mnt"} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			candidates = append(candidates, filepath.Join(base, e.Name()))
		}
	}
	mounts := readMounts()
	for _, c := range candidates {
		// A mount point from the config is trusted even if it is not a mount
		// (e.g. a bind mount or a copy of the Kobo's files).
		dev, mounted := mounts[c]
		if !mounted && c != extra {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, ".kobo")); err != nil || !st.IsDir() {
			continue
		}
		d := &Device{Mount: c, Dev: dev}
		d.readVersion()
		return d
	}
	return nil
}

// readMounts maps mount points to their devices from /proc/self/mounts.
func readMounts() map[string]string {
	m := map[string]string{}
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return m
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) >= 2 {
			m[unescapeMount(fields[1])] = unescapeMount(fields[0])
		}
	}
	return m
}

// unescapeMount decodes the octal escapes (\040 for space) used in /proc/mounts.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var c int
			if _, err := fmt.Sscanf(s[i+1:i+4], "%o", &c); err == nil {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (d *Device) readVersion() {
	data, err := os.ReadFile(filepath.Join(d.Mount, ".kobo", "version"))
	if err != nil {
		return
	}
	parts := strings.Split(strings.TrimSpace(string(data)), ",")
	if len(parts) >= 3 {
		d.Firmware = safe.Text(parts[2])
	}
}

// Space returns free and total bytes on the Kobo.
func (d *Device) Space() (free, total uint64) {
	var st syscall.Statfs_t
	if syscall.Statfs(d.Mount, &st) != nil {
		return 0, 0
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize)
}

// Reading status values used by the Kobo database.
const (
	Unread   = 0
	Reading  = 1
	Finished = 2
)

// Progress is the reading state of one book on the Kobo.
type Progress struct {
	Percent  int
	Status   int
	LastRead time.Time
}

// Progress reads the reading state of sideloaded books from KoboReader.sqlite,
// keyed by path relative to the Kobo's root. The database is opened read only.
func (d *Device) Progress() (map[string]Progress, error) {
	dbPath := filepath.Join(d.Mount, ".kobo", "KoboReader.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT ContentID, ___PercentRead, ReadStatus, DateLastRead FROM content WHERE ContentType = 6`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	const prefix = "file:///mnt/onboard/"
	out := map[string]Progress{}
	for rows.Next() {
		var id string
		var pct, status sql.NullInt64
		var last sql.NullString
		if err := rows.Scan(&id, &pct, &status, &last); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		p := Progress{Percent: int(pct.Int64), Status: int(status.Int64)}
		if last.Valid {
			for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000", "2006-01-02T15:04:05"} {
				if t, err := time.Parse(layout, last.String); err == nil {
					p.LastRead = t
					break
				}
			}
		}
		out[strings.TrimPrefix(id, prefix)] = p
	}
	return out, rows.Err()
}

// Eject flushes writes, unmounts the Kobo and powers it off so it imports the
// new books.
func (d *Device) Eject() error {
	syscall.Sync()
	if d.Dev == "" || !strings.HasPrefix(d.Dev, "/dev/") {
		return fmt.Errorf("unknown block device for %s", d.Mount)
	}
	if out, err := exec.Command("udisksctl", "unmount", "-b", d.Dev).CombinedOutput(); err != nil {
		return fmt.Errorf("unmount: %s", strings.TrimSpace(string(out)))
	}
	// Power off is best effort; the Kobo picks up the books either way.
	_ = exec.Command("udisksctl", "power-off", "-b", d.Dev).Run()
	return nil
}

// ManifestEntry records which library file a synced Kobo file came from.
type ManifestEntry struct {
	Source  string `json:"source"` // path relative to the library root
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
}

// Manifest maps Kobo paths (relative to its root) to their source.
type Manifest map[string]ManifestEntry

const manifestName = ".kobosync.json"

// LoadManifest reads the sync manifest kept on the Kobo.
func (d *Device) LoadManifest() Manifest {
	m := Manifest{}
	if data, err := os.ReadFile(filepath.Join(d.Mount, manifestName)); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

// SaveManifest writes the sync manifest to the Kobo.
func (d *Device) SaveManifest(m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := filepath.Join(d.Mount, manifestName)
	if err := os.WriteFile(p+".tmp", data, 0o644); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}
