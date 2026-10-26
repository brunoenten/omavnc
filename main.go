package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"gioui.org/app"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/brunoenten/omavnc/internal/config"
	"github.com/brunoenten/omavnc/internal/hub"
	"github.com/brunoenten/omavnc/internal/theme"
	"github.com/brunoenten/omavnc/internal/tui"
)

func main() {
	password := flag.String("password", "", "VNC password (or OMAVNC_PASSWORD)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: omavnc [flags] [host[:port]]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	host := flag.Arg(0)
	pass := *password
	if pass == "" {
		pass = os.Getenv("OMAVNC_PASSWORD")
	}

	cfg := config.Load()
	h := hub.New(&cfg)

	go func() {
		code := run(h, &cfg, host, pass)
		os.Exit(code)
	}()
	app.Main()
}

func run(h *hub.Hub, cfg *config.Config, host, pass string) int {
	m := tui.New(h, cfg, host, pass)
	p := tea.NewProgram(m, tea.WithAltScreen())
	h.SetProgram(p)

	theme.Watch(func() {
		p.Send(tui.ThemeMsg())
	})

	// Pre-fill password into the model by connecting if both host and pass given
	// via CLI — still show TUI; auto-connect when host is provided.
	if strings.TrimSpace(host) != "" {
		go func() {
			// slight delay so the program is ready to receive StatusMsg
			h.Connect(host, pass)
		}()
	} else if pass != "" {
		// password without host: stash via env already consumed; user types host
		_ = pass
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "omavnc: %v\n", err)
		h.Shutdown()
		return 1
	}
	h.Shutdown()
	return 0
}
