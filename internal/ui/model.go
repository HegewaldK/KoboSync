// Package ui implements the kobosync terminal interface.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"kobosync/internal/config"
	"kobosync/internal/epub"
	"kobosync/internal/i18n"
	"kobosync/internal/kobo"
	"kobosync/internal/library"
	"kobosync/internal/safe"
	"kobosync/internal/syncer"
)

type mode int

const (
	modeNormal  mode = iota
	modePath         // choosing the library folder
	modeConfirm      // asking before a sync
	modeSync         // sync running
)

var sortModes = []string{"author", "title", "status", "progress"}

type Model struct {
	cfg     config.Config
	cache   *library.Cache
	locales *i18n.Set
	loc     *i18n.Locale

	width, height int
	mode          mode

	lib        []library.Book
	libLoading bool

	dev        *kobo.Device
	devBooks   []library.Book
	devLoading bool
	manifest   kobo.Manifest
	progress   map[string]kobo.Progress
	free       uint64
	total      uint64

	entries   []syncer.Entry
	view      []int // indices into entries after filtering and sorting
	cursor    int
	offset    int
	overrides map[string]bool // Want changes the user made, by entry key
	sortBy    int

	filter    textinput.Model
	filtering bool

	pathInput textinput.Model
	pathHint  string

	syncCh     chan tea.Msg
	cancelSync context.CancelFunc
	syncDone   int
	syncTotal  int
	syncName   string
	syncErrs   []string

	status    string
	statusErr bool
	statusAt  time.Time
	ejected   bool // the Kobo vanished because we ejected it

	covers       map[string]coverResult
	coverPending map[string]bool
	coverMode    coverMode
	nextImageID  uint32 // Kitty image ids
}

type coverResult struct {
	art string
	err error
}

type (
	tickMsg   struct{}
	detectMsg struct {
		dev         *kobo.Device
		free, total uint64
	}
	libLoadedMsg struct {
		root  string
		books []library.Book
		err   error
	}
	devLoadedMsg struct {
		mount    string
		books    []library.Book
		manifest kobo.Manifest
		progress map[string]kobo.Progress
		err      error
	}
	coverMsg struct {
		key string
		res coverResult
	}
	syncEventMsg syncer.Event
	syncDoneMsg  struct{ err error }
	ejectMsg     struct{ err error }
)

// New creates the UI model.
func New(cfg config.Config, cache *library.Cache, locales *i18n.Set) Model {
	loc := locales.Get(cfg.Language)
	f := textinput.New()
	f.Prompt = "/ "
	f.Placeholder = loc.T("filter.placeholder")
	f.PromptStyle = keyStyle
	f.TextStyle = textStyle

	p := textinput.New()
	p.Prompt = "› "
	p.Placeholder = "~/Books"
	p.PromptStyle = keyStyle
	p.TextStyle = textStyle

	m := Model{
		cfg:          cfg,
		cache:        cache,
		locales:      locales,
		loc:          loc,
		overrides:    map[string]bool{},
		filter:       f,
		pathInput:    p,
		covers:       map[string]coverResult{},
		coverPending: map[string]bool{},
	}
	if cfg.Library == "" {
		m.openPathInput()
	} else {
		m.libLoading = true
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.detect(), tick()}
	if m.cfg.Library != "" {
		cmds = append(cmds, loadLibrary(m.cfg.Library, m.cache))
	}
	if m.mode == modePath {
		cmds = append(cmds, textinput.Blink)
	}
	return tea.Batch(cmds...)
}

// ---- commands ----

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) detect() tea.Cmd {
	mount := m.cfg.Mount
	return func() tea.Msg {
		d := kobo.Detect(mount)
		msg := detectMsg{dev: d}
		if d != nil {
			msg.free, msg.total = d.Space()
		}
		return msg
	}
}

func loadLibrary(root string, cache *library.Cache) tea.Cmd {
	return func() tea.Msg {
		books, err := library.Scan(root, cache)
		_ = cache.Save()
		return libLoadedMsg{root: root, books: books, err: err}
	}
}

func loadDevice(d *kobo.Device, cache *library.Cache) tea.Cmd {
	return func() tea.Msg {
		books, err := library.Scan(d.Mount, cache)
		_ = cache.Save()
		msg := devLoadedMsg{mount: d.Mount, books: books, manifest: d.LoadManifest(), err: err}
		if p, perr := d.Progress(); perr == nil {
			msg.progress = p
		}
		return msg
	}
}

func loadCover(key string, b *library.Book, w, rows int, mode coverMode, id uint32) tea.Cmd {
	path, meta, coverFile := b.Path, b.Meta, b.CoverFile
	return func() tea.Msg {
		var img image.Image
		var err error
		if coverFile != "" {
			img, err = epub.CoverFile(coverFile)
		} else {
			img, err = epub.Cover(path, meta)
		}
		if err != nil {
			return coverMsg{key: key, res: coverResult{err: err}}
		}
		return coverMsg{key: key, res: coverResult{art: renderCover(mode, img, w, rows, id)}}
	}
}

func waitSync(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// ---- update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, m.ensureCover()

	case tickMsg:
		return m, tea.Batch(m.detect(), tick())

	case detectMsg:
		return m.handleDetect(msg)

	case libLoadedMsg:
		if msg.root != m.cfg.Library {
			return m, nil // stale result from a previous library
		}
		m.libLoading = false
		m.lib = msg.books
		if msg.err != nil {
			m.setStatus(m.loc.F("msg.cannot_read_library", msg.err), true)
		}
		m.rebuild()
		return m, m.ensureCover()

	case devLoadedMsg:
		if m.dev == nil || msg.mount != m.dev.Mount {
			return m, nil
		}
		m.devLoading = false
		m.devBooks, m.manifest, m.progress = msg.books, msg.manifest, msg.progress
		if msg.err != nil {
			m.setStatus(m.loc.F("msg.cannot_read_kobo", msg.err), true)
		}
		m.rebuild()
		return m, m.ensureCover()

	case coverMsg:
		delete(m.coverPending, msg.key)
		m.covers[msg.key] = msg.res
		return m, nil

	case syncEventMsg:
		m.syncDone, m.syncTotal = msg.Done, msg.Total
		if msg.Entry != nil {
			m.syncName = msg.Entry.Book().Title()
		}
		if msg.Err != nil {
			m.syncErrs = append(m.syncErrs, fmt.Sprintf("%s: %v", m.syncName, msg.Err))
		}
		return m, waitSync(m.syncCh)

	case syncDoneMsg:
		return m.finishSync(msg.err)

	case ejectMsg:
		if msg.err != nil {
			m.setStatus(m.loc.F("msg.eject_failed", msg.err), true)
			return m, nil
		}
		m.ejected = true
		m.setStatus(m.loc.T("msg.ejected"), false)
		return m, m.detect()

	case tea.MouseMsg:
		if m.mode == modeNormal && !m.filtering {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.move(-3)
			case tea.MouseButtonWheelDown:
				m.move(3)
			}
			return m, m.ensureCover()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleDetect(msg detectMsg) (tea.Model, tea.Cmd) {
	m.free, m.total = msg.free, msg.total
	switch {
	case msg.dev == nil && m.dev != nil:
		m.dev, m.devBooks, m.manifest, m.progress = nil, nil, nil, nil
		m.devLoading = false
		if m.mode == modeConfirm {
			m.mode = modeNormal
		}
		if !m.ejected {
			m.setStatus(m.loc.T("msg.disconnected"), false)
		}
		m.ejected = false
		m.rebuild()
	case msg.dev != nil && (m.dev == nil || m.dev.Mount != msg.dev.Mount):
		m.dev = msg.dev
		m.devLoading = true
		m.ejected = false
		m.setStatus(m.loc.F("msg.connected", msg.dev.Mount), false)
		return m, loadDevice(m.dev, m.cache)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		if m.cancelSync != nil {
			m.cancelSync()
		}
		return m, tea.Quit
	}

	switch m.mode {
	case modePath:
		return m.handlePathKey(msg)
	case modeConfirm:
		switch msg.String() {
		case "y", "Y", "j", "J", "enter":
			return m.startSync()
		case "n", "N", "esc", "q":
			m.mode = modeNormal
		}
		return m, nil
	case modeSync:
		if msg.String() == "esc" && m.cancelSync != nil {
			m.cancelSync()
			m.setStatus(m.loc.T("msg.cancelling"), false)
		}
		return m, nil
	}

	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.SetValue("")
			m.applyView()
			return m, m.ensureCover()
		case "enter", "down", "up":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.applyView()
		return m, tea.Batch(cmd, m.ensureCover())
	}

	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight())
	case "pgdown", "ctrl+d":
		m.move(m.listHeight())
	case "home", "g":
		m.move(-len(m.view))
	case "end", "G":
		m.move(len(m.view))
	case " ":
		if e := m.current(); e != nil {
			if e.Kobo == nil && e.Lib.Err != "" {
				m.setStatus(m.loc.T("msg.unreadable"), true)
				return m, nil
			}
			e.Want = !e.Want
			if e.Want == (e.Kobo != nil) {
				delete(m.overrides, e.Key())
			} else {
				m.overrides[e.Key()] = e.Want
			}
			m.move(1)
		}
	case "a":
		n := 0
		for _, i := range m.view {
			e := &m.entries[i]
			if e.Lib != nil && e.Lib.Err == "" && e.Kobo == nil && !e.Want {
				e.Want = true
				m.overrides[e.Key()] = true
				n++
			}
		}
		m.setStatus(m.loc.F("msg.marked", n), false)
	case "u":
		m.overrides = map[string]bool{}
		m.rebuild()
		m.setStatus(m.loc.T("msg.discarded"), false)
	case "s":
		if m.dev == nil {
			m.setStatus(m.loc.T("msg.no_kobo"), true)
		} else if m.devLoading {
			m.setStatus(m.loc.T("msg.kobo_busy"), true)
		} else if add, upd, rem := m.pending(); add+upd+rem == 0 {
			m.setStatus(m.loc.T("msg.nothing"), false)
		} else {
			m.mode = modeConfirm
		}
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case "esc":
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.applyView()
		}
	case "o":
		m.sortBy = (m.sortBy + 1) % len(sortModes)
		m.applyView()
		m.setStatus(m.loc.F("msg.sorted", m.loc.T("sort."+sortModes[m.sortBy])), false)
	case "c":
		m.coverMode = (m.coverMode + 1) % coverModes
		m.setStatus(m.loc.F("msg.cover", m.loc.T("cover.mode."+coverModeKeys[m.coverMode])), false)
	case "L":
		m.loc = m.locales.Next(m.loc.Code)
		m.cfg.Language = m.loc.Code
		m.filter.Placeholder = m.loc.T("filter.placeholder")
		if err := config.Save(m.cfg); err != nil {
			m.setStatus(m.loc.F("msg.config_error", err), true)
		} else {
			m.setStatus(m.loc.F("msg.language", m.loc.Name()), false)
		}
	case "l":
		m.openPathInput()
		return m, textinput.Blink
	case "r":
		m.libLoading = true
		cmds := []tea.Cmd{loadLibrary(m.cfg.Library, m.cache)}
		if m.dev != nil {
			m.devLoading = true
			cmds = append(cmds, loadDevice(m.dev, m.cache))
		}
		m.setStatus(m.loc.T("msg.rescanning"), false)
		return m, tea.Batch(cmds...)
	case "e":
		if m.dev == nil {
			m.setStatus(m.loc.T("msg.no_kobo"), true)
			return m, nil
		}
		d := m.dev
		m.setStatus(m.loc.T("msg.ejecting"), false)
		return m, func() tea.Msg { return ejectMsg{err: d.Eject()} }
	}
	return m, m.ensureCover()
}

func (m *Model) openPathInput() {
	m.mode = modePath
	m.pathHint = ""
	v := m.cfg.Library
	if home, err := os.UserHomeDir(); err == nil && v != "" && strings.HasPrefix(v, home) {
		v = "~" + strings.TrimPrefix(v, home)
	}
	m.pathInput.SetValue(v)
	m.pathInput.CursorEnd()
	m.pathInput.Focus()
}

func (m Model) handlePathKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.cfg.Library == "" {
			return m, tea.Quit
		}
		m.mode = modeNormal
		m.pathInput.Blur()
		return m, nil
	case "tab":
		v, matches := completeDir(m.pathInput.Value())
		m.pathInput.SetValue(v)
		m.pathInput.CursorEnd()
		m.pathHint = ""
		if len(matches) > 1 {
			m.pathHint = safe.Text(strings.Join(matches, "  "))
		}
		return m, nil
	case "enter":
		p := config.ExpandHome(strings.TrimSpace(m.pathInput.Value()))
		if p == "" {
			m.pathHint = "✗ " + m.loc.T("path.enter_folder")
			return m, nil
		}
		abs, err := filepath.Abs(p)
		if err == nil {
			var st os.FileInfo
			if st, err = os.Stat(abs); err == nil && !st.IsDir() {
				err = errors.New(m.loc.T("path.not_folder"))
			}
		}
		if err != nil {
			m.pathHint = "✗ " + err.Error()
			return m, nil
		}
		m.cfg.Library = abs
		if err := config.Save(m.cfg); err != nil {
			m.setStatus(m.loc.F("msg.config_error", err), true)
		}
		m.mode = modeNormal
		m.pathInput.Blur()
		m.lib, m.libLoading = nil, true
		m.overrides = map[string]bool{}
		m.rebuild()
		return m, loadLibrary(abs, m.cache)
	}
	var cmd tea.Cmd
	m.pathInput, cmd = m.pathInput.Update(msg)
	return m, cmd
}

// completeDir completes the last path element to matching folders.
func completeDir(s string) (string, []string) {
	if s == "~" {
		return "~/", nil
	}
	dir, prefix := filepath.Split(config.ExpandHome(s))
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return s, nil
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && st.IsDir() {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return s, nil
	}
	common := matches[0]
	for _, mt := range matches[1:] {
		for !strings.HasPrefix(mt, common) {
			common = common[:len(common)-1]
		}
	}
	out := s[:len(s)-len(prefix)] + common
	if len(matches) == 1 {
		out += "/"
	}
	return out, matches
}

func (m Model) startSync() (tea.Model, tea.Cmd) {
	var todo []*syncer.Entry
	for i := range m.entries {
		if m.entries[i].Action() != syncer.None {
			todo = append(todo, &m.entries[i])
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, 8)
	m.mode = modeSync
	m.syncCh, m.cancelSync = ch, cancel
	m.syncDone, m.syncTotal, m.syncName, m.syncErrs = 0, len(todo), "", nil
	dev, dir := m.dev, m.cfg.KoboDir
	go func() {
		err := syncer.Apply(ctx, dev, dir, todo, func(ev syncer.Event) { ch <- syncEventMsg(ev) })
		ch <- syncDoneMsg{err: err}
		close(ch)
	}()
	return m, waitSync(ch)
}

func (m Model) finishSync(err error) (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	m.cancelSync, m.syncCh = nil, nil
	m.overrides = map[string]bool{}
	switch {
	case err != nil:
		m.setStatus(m.loc.F("msg.sync_stopped", m.syncDone, m.syncTotal, err), true)
	case len(m.syncErrs) > 0:
		m.setStatus(m.loc.F("msg.sync_failed", len(m.syncErrs), m.syncTotal, m.syncErrs[len(m.syncErrs)-1]), true)
	default:
		m.setStatus(m.loc.F("msg.synced", m.syncTotal), false)
	}
	if m.dev == nil {
		return m, nil
	}
	m.devLoading = true
	return m, loadDevice(m.dev, m.cache)
}

// ---- list state ----

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.statusErr, m.statusAt = safe.Text(s), isErr, time.Now()
}

func (m *Model) rebuild() {
	var key string
	if e := m.current(); e != nil {
		key = e.Key()
	}
	m.entries = syncer.Match(m.lib, m.devBooks, m.manifest, m.progress)
	for i := range m.entries {
		if v, ok := m.overrides[m.entries[i].Key()]; ok {
			m.entries[i].Want = v
		}
	}
	m.applyView()
	for i, idx := range m.view {
		if m.entries[idx].Key() == key {
			m.cursor = i
			break
		}
	}
	m.clampScroll()
}

func (m *Model) applyView() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.view = m.view[:0]
	for i := range m.entries {
		if q == "" || strings.Contains(searchText(&m.entries[i]), q) {
			m.view = append(m.view, i)
		}
	}
	less := m.lessFunc()
	sort.SliceStable(m.view, func(a, b int) bool { return less(&m.entries[m.view[a]], &m.entries[m.view[b]]) })
	m.clampScroll()
}

func searchText(e *syncer.Entry) string {
	b := e.Book()
	return strings.ToLower(strings.Join([]string{b.Title(), b.Author(), b.Meta.Series, b.Meta.Publisher, b.Meta.ISBN, b.Rel, b.AllFormats()}, "\x00"))
}

func (m *Model) lessFunc() func(a, b *syncer.Entry) bool {
	byAuthorTitle := func(a, b *syncer.Entry) bool {
		aa, ba := a.Book().AuthorSort(), b.Book().AuthorSort()
		if aa != ba {
			if aa == "" || ba == "" {
				return ba == "" // books without author go last
			}
			return aa < ba
		}
		return seriesTitle(a) < seriesTitle(b)
	}
	switch sortModes[m.sortBy] {
	case "title":
		return func(a, b *syncer.Entry) bool {
			return strings.ToLower(a.Book().Title()) < strings.ToLower(b.Book().Title())
		}
	case "status":
		return func(a, b *syncer.Entry) bool {
			if ra, rb := statusRank(a), statusRank(b); ra != rb {
				return ra < rb
			}
			return byAuthorTitle(a, b)
		}
	case "progress":
		return func(a, b *syncer.Entry) bool {
			ta, tb := lastRead(a), lastRead(b)
			if !ta.Equal(tb) {
				return ta.After(tb)
			}
			return byAuthorTitle(a, b)
		}
	}
	return byAuthorTitle
}

// seriesTitle sorts books of a series by their index.
func seriesTitle(e *syncer.Entry) string {
	b := e.Book()
	if b.Meta.Series != "" {
		return fmt.Sprintf("%s\x00%08s", strings.ToLower(b.Meta.Series), b.Meta.SeriesIndex)
	}
	return strings.ToLower(b.Title())
}

func lastRead(e *syncer.Entry) time.Time {
	if e.Progress != nil {
		return e.Progress.LastRead
	}
	return time.Time{}
}

func statusRank(e *syncer.Entry) int {
	switch {
	case e.Action() != syncer.None:
		return 0
	case e.Lib == nil:
		return 1
	case e.Kobo != nil:
		return 2
	}
	return 3
}

func (m *Model) pending() (add, upd, rem int) {
	for i := range m.entries {
		switch m.entries[i].Action() {
		case syncer.Add:
			add++
		case syncer.Update:
			upd++
		case syncer.Remove:
			rem++
		}
	}
	return
}

func (m *Model) current() *syncer.Entry {
	if m.cursor < 0 || m.cursor >= len(m.view) {
		return nil
	}
	return &m.entries[m.view[m.cursor]]
}

func (m *Model) move(d int) {
	m.cursor += d
	m.clampScroll()
}

func (m *Model) clampScroll() {
	m.cursor = max(0, min(m.cursor, len(m.view)-1))
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if h > 0 && m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, len(m.view)-h))
}

// ensureCover starts loading the cover of the selected book if needed.
func (m *Model) ensureCover() tea.Cmd {
	e := m.current()
	if e == nil {
		return nil
	}
	w, rows := m.coverSize()
	if w < 4 || rows < 2 {
		return nil
	}
	b := e.Book()
	key := m.coverKey(b, w, rows)
	if _, ok := m.covers[key]; ok || m.coverPending[key] {
		return nil
	}
	m.coverPending[key] = true
	m.nextImageID++
	return loadCover(key, b, w, rows, m.coverMode, m.nextImageID)
}

func (m *Model) coverKey(b *library.Book, w, rows int) string {
	return fmt.Sprintf("%s|%s|%d|%dx%d|%d", b.Path, b.CoverFile, b.ModTime.Unix(), w, rows, m.coverMode)
}
