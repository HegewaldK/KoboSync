// kobosync syncs an EPUB library to a Kobo eReader, converting books to KEPUB.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"kobosync/internal/config"
	"kobosync/internal/i18n"
	"kobosync/internal/library"
	"kobosync/internal/ui"
)

const usage = `kobosync – sync an EPUB library to a Kobo eReader

Usage:
  kobosync [library-folder]

The library folder is remembered in %s.
`

func main() {
	if len(os.Args) > 2 || (len(os.Args) == 2 && (os.Args[1] == "-h" || os.Args[1] == "--help")) {
		fmt.Printf(usage, config.Path())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kobosync:", err)
		os.Exit(1)
	}
	if len(os.Args) == 2 {
		dir, err := filepath.Abs(config.ExpandHome(os.Args[1]))
		if err == nil {
			_, err = os.ReadDir(dir)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "kobosync:", err)
			os.Exit(1)
		}
		cfg.Library = dir
		if err := config.Save(cfg); err != nil {
			fmt.Fprintln(os.Stderr, "kobosync: cannot save config:", err)
		}
	}

	locales, errs := i18n.Load(filepath.Join(config.Dir(), "locales"))
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "kobosync: label file skipped:", err)
	}

	cache := library.OpenCache(config.CachePath())
	p := tea.NewProgram(ui.New(cfg, cache, locales), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kobosync:", err)
		os.Exit(1)
	}
	_ = cache.Save()
}
