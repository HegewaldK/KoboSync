package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"kobosync/internal/i18n"
	"kobosync/internal/kobo"
	"kobosync/internal/library"
	"kobosync/internal/safe"
	"kobosync/internal/syncer"
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.mode == modePath {
		return m.pathView()
	}
	l := m.layout()
	main := m.libraryPanel(l.list, l.mainH)
	if l.details > 0 {
		main = lipgloss.JoinHorizontal(lipgloss.Top, main, m.detailsPanel(l.details, l.mainH))
	}
	out := main + "\n"
	if l.actions {
		out += m.actionsPanel() + "\n"
	}
	out += m.statusBar() + "\n" + m.legend()
	if !l.actions {
		out += "\n" + m.helpBar()
	}
	return out
}

type layout struct {
	list, details int  // panel widths; 0 hides the details
	mainH         int  // height of the library and details panels
	actions       bool // actions panel at the bottom, else a one line help bar
}

const actionsH = 5 // borders, group titles and two rows of actions

func (m Model) layout() layout {
	l := layout{list: m.width}
	if m.width >= 90 {
		l.details = max(36, min(60, m.width*30/100))
	}
	l.list = m.width - l.details
	l.actions = m.actionsWidth() <= m.width-2 && m.height >= 24
	bottom := 3 // status, legend, help bar
	if l.actions {
		bottom = actionsH + 2
	}
	l.mainH = m.height - bottom
	return l
}

// panel draws a rounded box with the title set into the top border, like Superfile.
func panel(title string, lines []string, w, h int, focused bool) string {
	bs := borderStyle
	ts := titleDimStyle
	if focused {
		bs, ts = borderFocusStyle, titleStyle
	}
	iw, ih := w-2, h-2
	t := ts.Render(ansi.Truncate(title, max(0, iw-4), "…"))
	fill := max(0, iw-1-lipgloss.Width(t))
	var b strings.Builder
	b.WriteString(bs.Render("╭─") + t + bs.Render(strings.Repeat("─", fill)+"╮") + "\n")
	for i := 0; i < ih; i++ {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], iw, "…")
		}
		b.WriteString(bs.Render("│") + line + strings.Repeat(" ", max(0, iw-lipgloss.Width(line))) + bs.Render("│") + "\n")
	}
	b.WriteString(bs.Render("╰" + strings.Repeat("─", iw) + "╯"))
	return b.String()
}

// ---- actions ----

type action struct {
	key, label string
	enabled    bool
}

// actionGroup is a column of the actions panel: a title and two rows.
type actionGroup struct {
	title string
	rows  [2][]action
}

func (m Model) actions() []actionGroup {
	add, upd, rem := m.pending()
	hasKobo := m.dev != nil
	books := len(m.view) > 0
	return []actionGroup{
		{m.loc.T("act.group.sync"), [2][]action{
			{{"space", m.loc.T("act.toggle"), books}, {"a", m.loc.T("act.add_missing"), books}},
			{{"s", m.loc.T("act.sync"), hasKobo && add+upd+rem > 0}, {"u", m.loc.T("act.undo"), len(m.overrides) > 0}},
		}},
		{m.loc.T("act.group.view"), [2][]action{
			{{"/", m.loc.T("act.filter"), true}},
			{{"o", m.loc.F("act.sort", m.loc.T("sort."+sortModes[m.sortBy])), true}},
		}},
		{m.loc.T("act.group.library"), [2][]action{
			{{"l", m.loc.T("act.choose"), true}},
			{{"r", m.loc.T("act.rescan"), m.cfg.Library != ""}},
		}},
		{m.loc.T("act.group.kobo"), [2][]action{
			{{"e", m.loc.T("act.eject"), hasKobo}},
		}},
		{m.loc.T("act.group.general"), [2][]action{
			{{"L", m.loc.T("act.language") + ": " + m.loc.Name(), true}},
			{{"q", m.loc.T("act.quit"), true}},
		}},
	}
}

const groupGap = "     "

// groupColumns lays out one group: the widths of its keys and of its action
// columns, and its total width.
func groupColumns(g actionGroup) (keyW, colW []int, width int) {
	for _, row := range g.rows {
		for i, a := range row {
			if i == len(keyW) {
				keyW, colW = append(keyW, 0), append(colW, 0)
			}
			keyW[i] = max(keyW[i], ansi.StringWidth(a.key))
		}
	}
	for _, row := range g.rows {
		for i, a := range row {
			colW[i] = max(colW[i], keyW[i]+1+ansi.StringWidth(a.label))
		}
	}
	for i, w := range colW {
		if i > 0 {
			width += 3
		}
		width += w
	}
	return keyW, colW, max(width, ansi.StringWidth(g.title))
}

func (m Model) actionsWidth() int {
	w := 1
	for i, g := range m.actions() {
		if i > 0 {
			w += len(groupGap)
		}
		_, _, gw := groupColumns(g)
		w += gw
	}
	return w + 1
}

// actionsPanel shows the action groups side by side across the bottom.
func (m Model) actionsPanel() string {
	lines := []string{" ", " ", " "}
	for gi, g := range m.actions() {
		keyW, colW, gw := groupColumns(g)
		if gi > 0 {
			for i := range lines {
				lines[i] += groupGap
			}
		}
		lines[0] += headerStyle.Render(fit(g.title, gw))
		for r, row := range g.rows {
			cell := ""
			for i, a := range row {
				if i > 0 {
					cell += "   "
				}
				ks, ls := keyStyle, textStyle
				if !a.enabled {
					ks, ls = dimStyle, dimStyle
				}
				cell += ks.Render(fit(a.key, keyW[i])) + " " + ls.Render(fit(a.label, colW[i]-keyW[i]-1))
			}
			lines[r+1] += fit(cell, gw)
		}
	}
	return panel(m.loc.T("panel.actions"), lines, m.width, actionsH, false)
}

// helpBar replaces the actions panel on small screens.
func (m Model) helpBar() string {
	var parts []string
	if m.filtering {
		parts = append(parts, dimStyle.Render(m.loc.T("filter.hint")))
	} else {
		for _, g := range m.actions() {
			for _, row := range g.rows {
				for _, a := range row {
					parts = append(parts, keyStyle.Render(a.key)+" "+dimStyle.Render(a.label))
				}
			}
		}
	}
	return ansi.Truncate(" "+strings.Join(parts, "  "), m.width, "")
}

// ---- library ----

// listHeight is the number of book rows that fit into the library panel.
func (m Model) listHeight() int {
	h := m.layout().mainH - 2 - 1 // borders, column header
	if m.filtering || m.filter.Value() != "" {
		h--
	}
	return max(1, h)
}

// formats returns the format in the library and on the Kobo. For a book
// marked for adding, the Kobo format is the one it will get.
func formats(e *syncer.Entry) (lib, dev string) {
	if e.Lib != nil {
		lib = e.Lib.Kind()
	}
	switch {
	case e.Kobo != nil:
		dev = e.Kobo.Kind()
	case e.Want && e.Lib != nil:
		dev = library.Kind("x" + syncer.TargetExt(e.Lib))
	}
	return lib, dev
}

func (m Model) progressLabel(p *kobo.Progress) string {
	switch {
	case p == nil:
		return ""
	case p.Status == kobo.Finished:
		return m.loc.T("progress.finished")
	case p.Status == kobo.Reading || p.Percent > 0:
		return fmt.Sprintf("%d%%", p.Percent)
	}
	return ""
}

func (m Model) libraryPanel(w, h int) string {
	iw := w - 2
	title := m.loc.T("panel.library")
	if m.cfg.Library != "" {
		title += " · " + shortPath(m.cfg.Library)
	}
	if len(m.entries) > 0 {
		if len(m.view) != len(m.entries) {
			title += fmt.Sprintf(" · %d/%d", len(m.view), len(m.entries))
		} else {
			title += fmt.Sprintf(" · %d", len(m.entries))
		}
	}

	var lines []string
	if m.filtering || m.filter.Value() != "" {
		hint := ""
		if m.filtering {
			hint = m.loc.T("filter.hint")
		}
		m.filter.Width = iw - 6 - ansi.StringWidth(hint)
		lines = append(lines, " "+fit(m.filter.View(), iw-3-ansi.StringWidth(hint))+dimStyle.Render(hint))
	}

	hTitle, hAuthor, hFormat, hKobo, hLang, hProg := m.loc.T("col.title"), m.loc.T("col.author"), m.loc.T("col.format"),
		m.loc.T("col.format_kobo"), m.loc.T("col.language"), m.loc.T("col.progress")
	end := min(len(m.view), m.offset+m.listHeight())
	formatW := max(ansi.StringWidth(hFormat), len(library.KEPUB))
	koboW := max(ansi.StringWidth(hKobo), len(library.KEPUB))
	langW := ansi.StringWidth(hLang)
	progW := max(ansi.StringWidth(hProg), ansi.StringWidth(m.loc.T("progress.finished")), 4)
	const gap = "   " // between columns
	flex := iw - 4 - formatW - koboW - langW - progW - 5*len(gap) - 1
	titleW := max(10, flex*58/100)
	authorW := max(8, flex-titleW)
	row := func(icon, title, author, format, kobo, lang, prog string) string {
		return " " + icon + "  " + fit(title, titleW) + gap + fit(author, authorW) + gap + fit(format, formatW) + gap +
			fit(kobo, koboW) + gap + fit(lang, langW) + gap + fitRight(prog, progW) + " "
	}
	lines = append(lines, headerStyle.Render(row(" ", hTitle, hAuthor, hFormat, hKobo, hLang, hProg)))

	switch {
	case m.cfg.Library == "":
		lines = append(lines, "", dimStyle.Render("  "+m.loc.T("empty.no_library")))
	case m.libLoading && len(m.lib) == 0:
		lines = append(lines, "", dimStyle.Render("  "+m.loc.T("empty.scanning")))
	case len(m.entries) == 0:
		lines = append(lines, "", dimStyle.Render("  "+m.loc.F("empty.no_books", shortPath(m.cfg.Library))))
	case len(m.view) == 0:
		lines = append(lines, "", dimStyle.Render("  "+m.loc.T("empty.no_match")))
	}

	for i := m.offset; i < end; i++ {
		e := &m.entries[m.view[i]]
		b := e.Book()
		icon, _, color := m.entryStatus(e)
		title := b.Title()
		series := ""
		if b.Meta.Series != "" && b.Meta.SeriesIndex != "" {
			series = fmt.Sprintf(" (%s #%s)", b.Meta.Series, b.Meta.SeriesIndex)
		}
		lang := i18n.LangCode(b.Meta.Language)
		prog := m.progressLabel(e.Progress)
		format, kobo := formats(e)
		if i == m.cursor {
			lines = append(lines, selectedStyle.Width(iw).Render(row(icon, title+series, b.Author(), format, kobo, lang, prog)))
			continue
		}
		titleCell := fit(textStyle.Render(title)+dimStyle.Render(series), titleW)
		line := " " + lipgloss.NewStyle().Foreground(color).Render(icon) + "  " +
			titleCell + gap +
			authorStyle.Render(fit(b.Author(), authorW)) + gap +
			subStyle.Render(fit(format, formatW)) + gap +
			subStyle.Render(fit(kobo, koboW)) + gap +
			subStyle.Render(fit(lang, langW)) + gap +
			progressStyle(e.Progress).Render(fitRight(prog, progW))
		lines = append(lines, line)
	}
	return panel(title, lines, w, h, true)
}

// entryStatus returns the icon, label and colour for an entry.
func (m Model) entryStatus(e *syncer.Entry) (string, string, lipgloss.Color) {
	switch e.Action() {
	case syncer.Add:
		return "+", m.loc.T("status.add"), cBlue
	case syncer.Update:
		return "↻", m.loc.T("status.update"), cYellow
	case syncer.Remove:
		return "−", m.loc.T("status.remove"), cRed
	}
	switch {
	case m.dev == nil:
		return "·", m.loc.T("status.disconnected"), cOverlay
	case e.Lib == nil:
		return "◆", m.loc.T("status.kobo_only"), cMauve
	case e.Kobo != nil:
		return "✓", m.loc.T("status.on_kobo"), cGreen
	}
	return "○", m.loc.T("status.missing"), cOverlay
}

func progressStyle(p *kobo.Progress) lipgloss.Style {
	if p != nil && p.Status == kobo.Finished {
		return okStyle
	}
	return lipgloss.NewStyle().Foreground(cPeach)
}

// ---- details ----

// coverSize is the size of the cover area in the details panel.
func (m Model) coverSize() (w, rows int) {
	l := m.layout()
	if l.details == 0 {
		return 0, 0
	}
	iw, ih := l.details-4, l.mainH-2
	rows = min(ih*45/100, iw*3/4) // a 2:3 cover is rows*4/3 cells wide
	return iw, rows
}

func (m Model) detailsPanel(w, h int) string {
	iw := w - 2
	e := m.current()
	if e == nil {
		return panel(m.loc.T("panel.details"), []string{"", dimStyle.Render(" " + m.loc.T("empty.no_selection"))}, w, h, false)
	}
	b := e.Book()
	var lines []string

	cw, rows := m.coverSize()
	if rows >= 2 {
		key := m.coverKey(b, cw, rows)
		var coverLines []string
		if res, ok := m.covers[key]; ok && res.err == nil && res.art != "" {
			coverLines = strings.Split(res.art, "\n")
		} else {
			coverLines = m.placeholder(min(cw, rows*4/3), rows, m.coverPending[key])
		}
		pad := strings.Repeat(" ", max(0, (iw-lipgloss.Width(coverLines[0]))/2))
		for _, l := range coverLines {
			lines = append(lines, pad+l)
		}
		lines = append(lines, "")
	}

	for _, l := range wrap(b.Title(), iw-2, 3) {
		lines = append(lines, " "+bookTitleStyle.Render(l))
	}
	if a := b.Author(); a != "" {
		lines = append(lines, " "+authorStyle.Render(ansi.Truncate(a, iw-2, "…")))
	}
	lines = append(lines, "")

	labelW := 0
	for _, k := range []string{"series", "publisher", "language", "isbn", "format", "formats", "on_kobo", "status", "progress", "last_read", "file", "kobo_file"} {
		labelW = max(labelW, ansi.StringWidth(m.loc.T("field."+k)))
	}
	field := func(key, value string, style lipgloss.Style) {
		if value != "" {
			lines = append(lines, " "+labelStyle.Render(fit(m.loc.T("field."+key), labelW))+" "+style.Render(value))
		}
	}
	if b.Meta.Series != "" {
		s := b.Meta.Series
		if b.Meta.SeriesIndex != "" {
			s += " #" + b.Meta.SeriesIndex
		}
		field("series", s, textStyle)
	}
	field("publisher", b.Meta.Publisher, textStyle)
	if name := m.loc.LanguageName(b.Meta.Language); name != "" {
		field("language", name, textStyle)
	}
	field("isbn", b.Meta.ISBN, textStyle)
	if e.Lib != nil {
		field("format", e.Lib.Format()+" · "+m.loc.Bytes(uint64(e.Lib.Size)), textStyle)
		if len(e.Lib.Formats) > 1 {
			field("formats", e.Lib.AllFormats(), textStyle)
		}
		field("file", safe.Text(e.Lib.Rel), subStyle)
	}

	lines = append(lines, "")
	icon, label, color := m.entryStatus(e)
	field("status", icon+" "+label, lipgloss.NewStyle().Foreground(color))
	if p := e.Progress; p != nil {
		switch {
		case p.Status == kobo.Finished:
			field("progress", "✓ "+m.loc.T("progress.finished"), okStyle)
		case p.Status == kobo.Reading || p.Percent > 0:
			lines = append(lines, " "+labelStyle.Render(fit(m.loc.T("field.progress"), labelW))+" "+bar(p.Percent, 12)+" "+textStyle.Render(fmt.Sprintf("%d%%", p.Percent)))
		default:
			field("progress", m.loc.T("progress.unread"), subStyle)
		}
		if !p.LastRead.IsZero() {
			field("last_read", m.loc.Date(p.LastRead.Local()), textStyle)
		}
	}
	if e.Kobo != nil {
		field("on_kobo", e.Kobo.Format()+" · "+m.loc.Bytes(uint64(e.Kobo.Size)), textStyle)
		field("kobo_file", safe.Text(e.Kobo.Rel), subStyle)
	}
	if b.Err != "" {
		lines = append(lines, " "+errStyle.Render(m.loc.F("detail.metadata_error", safe.Text(b.Err))))
	}

	if b.Meta.Description != "" && len(lines) < h-4 {
		lines = append(lines, "")
		for _, l := range wrap(b.Meta.Description, iw-2, h-2-len(lines)) {
			lines = append(lines, " "+subStyle.Render(l))
		}
	}
	return panel(m.loc.T("panel.details"), lines, w, h, false)
}

func (m Model) placeholder(w, rows int, loading bool) []string {
	w = max(w, 8)
	box := lipgloss.NewStyle().Foreground(cOverlay)
	out := make([]string, rows)
	for i := range out {
		out[i] = box.Render(strings.Repeat("░", w))
	}
	msg := " " + m.loc.T("cover.none") + " "
	if loading {
		msg = " " + m.loc.T("cover.loading") + " "
	}
	if mw := ansi.StringWidth(msg); rows > 0 && w > mw {
		pad := (w - mw) / 2
		out[rows/2] = box.Render(strings.Repeat("░", pad)) + dimStyle.Render(msg) + box.Render(strings.Repeat("░", w-pad-mw))
	}
	return out
}

// ---- bottom lines ----

func (m Model) statusBar() string {
	var left string
	if m.dev != nil {
		info := safe.Text(shortPath(m.dev.Mount))
		if m.dev.Firmware != "" {
			info = m.loc.F("bar.firmware", m.dev.Firmware) + " · " + info
		}
		if m.total > 0 {
			info += " · " + m.loc.F("bar.free", m.loc.Bytes(m.free), m.loc.Bytes(m.total))
		}
		if m.devLoading {
			info += " · " + m.loc.T("bar.reading")
		}
		left = badgeOnStyle.Render("● "+m.loc.T("bar.kobo")) + " " + subStyle.Render(info)
	} else {
		left = badgeOffStyle.Render("○ "+m.loc.T("bar.no_kobo")) + " " + dimStyle.Render(m.loc.T("bar.connect"))
	}

	var right string
	switch m.mode {
	case modeConfirm:
		add, upd, rem := m.pending()
		var parts []string
		if add > 0 {
			parts = append(parts, m.loc.F("bar.add_n", add))
		}
		if upd > 0 {
			parts = append(parts, m.loc.F("bar.update_n", upd))
		}
		if rem > 0 {
			parts = append(parts, errStyle.Render(m.loc.F("bar.delete_n", rem)))
		}
		right = badgeAskStyle.Render(m.loc.T("bar.sync_question")) + " " + strings.Join(parts, ", ") + "  " + keyStyle.Render("y") + dimStyle.Render("/") + keyStyle.Render("n")
	case modeSync:
		pct := 0
		if m.syncTotal > 0 {
			pct = m.syncDone * 100 / m.syncTotal
		}
		right = badgeSyncStyle.Render(m.loc.F("bar.syncing", m.syncDone, m.syncTotal)) + " " +
			bar(pct, 16) + " " + subStyle.Render(ansi.Truncate(m.syncName, 30, "…")) + "  " + dimStyle.Render(m.loc.T("bar.cancel"))
	default:
		if m.status != "" && time.Since(m.statusAt) < 6*time.Second {
			st := subStyle
			if m.statusErr {
				st = errStyle
			}
			right = st.Render(m.status)
		} else if add, upd, rem := m.pending(); add+upd+rem > 0 {
			right = lipgloss.NewStyle().Foreground(cBlue).Render(fmt.Sprintf("+%d", add)) + " " +
				lipgloss.NewStyle().Foreground(cYellow).Render(fmt.Sprintf("↻%d", upd)) + " " +
				lipgloss.NewStyle().Foreground(cRed).Render(fmt.Sprintf("−%d", rem)) + " " + dimStyle.Render(m.loc.T("bar.pending"))
		}
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if gap < 1 {
		right = ansi.Truncate(right, max(0, m.width-lipgloss.Width(left)-2), "…")
		gap = max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1)
	}
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, m.width, "")
}

// legend explains the status icons.
func (m Model) legend() string {
	items := []struct {
		icon, key string
		color     lipgloss.Color
	}{
		{"✓", "on_kobo", cGreen}, {"○", "missing", cOverlay}, {"+", "add", cBlue},
		{"↻", "update", cYellow}, {"−", "remove", cRed}, {"◆", "kobo_only", cMauve},
	}
	var parts []string
	for _, it := range items {
		parts = append(parts, lipgloss.NewStyle().Foreground(it.color).Render(it.icon)+" "+dimStyle.Render(m.loc.T("legend."+it.key)))
	}
	return ansi.Truncate(" "+strings.Join(parts, "   "), m.width, "")
}

// ---- library chooser ----

func (m Model) pathView() string {
	w := min(m.width, 80)
	iw := w - 2
	m.pathInput.Width = iw - 5
	lines := []string{"", " " + textStyle.Render(m.loc.T("path.question"))}
	for _, l := range wrap(m.loc.T("path.hint"), iw-2, 3) {
		lines = append(lines, " "+dimStyle.Render(l))
	}
	lines = append(lines, "", " "+m.pathInput.View(), "")
	if m.pathHint != "" {
		st := dimStyle
		if strings.HasPrefix(m.pathHint, "✗") {
			st = errStyle
		}
		for _, l := range wrap(m.pathHint, iw-2, 4) {
			lines = append(lines, " "+st.Render(l))
		}
		lines = append(lines, "")
	}
	esc := m.loc.T("key.cancel")
	if m.cfg.Library == "" {
		esc = m.loc.T("key.quit")
	}
	lines = append(lines, " "+keyStyle.Render("enter")+dimStyle.Render(" "+m.loc.T("key.select")+"  ")+
		keyStyle.Render("tab")+dimStyle.Render(" "+m.loc.T("key.complete")+"  ")+
		keyStyle.Render("esc")+dimStyle.Render(" "+esc))
	box := panel("kobosync · "+m.loc.T("panel.choose_library"), lines, w, len(lines)+2, true)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// ---- helpers ----

func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

func fitRight(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s
}

func wrap(s string, w, maxLines int) []string {
	if w < 1 || maxLines < 1 {
		return nil
	}
	lines := strings.Split(ansi.Wordwrap(s, w, ""), "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ansi.Truncate(lines[maxLines-1]+" …", w, "…")
	}
	return lines
}

func bar(pct, w int) string {
	n := max(0, min(w, pct*w/100))
	return okStyle.Render(strings.Repeat("━", n)) + dimStyle.Render(strings.Repeat("━", w-n))
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
