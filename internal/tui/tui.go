// Package tui is the bubbletea control panel.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brunoenten/omavnc/internal/config"
	"github.com/brunoenten/omavnc/internal/hub"
	"github.com/brunoenten/omavnc/internal/theme"
)

type screen int

const (
	screenConnect screen = iota
	screenSession
)

type styles struct {
	title, label, dim, accent, err, box, help lipgloss.Style
}

func makeStyles() styles {
	t := theme.Current()
	fg := theme.Hex(t.Fg)
	dim := theme.Hex(t.FgDim)
	accent := theme.Hex(t.Accent)
	bg := theme.Hex(t.Bg)
	red := theme.Hex(t.Red)
	return styles{
		title:  lipgloss.NewStyle().Foreground(lipgloss.Color(accent)).Bold(true),
		label:  lipgloss.NewStyle().Foreground(lipgloss.Color(fg)),
		dim:    lipgloss.NewStyle().Foreground(lipgloss.Color(dim)),
		accent: lipgloss.NewStyle().Foreground(lipgloss.Color(accent)),
		err:    lipgloss.NewStyle().Foreground(lipgloss.Color(red)),
		box:    lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(theme.Hex(t.Rule))).Padding(0, 1).Background(lipgloss.Color(bg)),
		help:   lipgloss.NewStyle().Foreground(lipgloss.Color(dim)),
	}
}

// Model is the bubbletea model.
type Model struct {
	h      *hub.Hub
	cfg    *config.Config
	screen screen
	status hub.Status
	styles styles

	hostInput textinput.Model
	passInput textinput.Model
	focus     int // 0 host, 1 pass
	quitting  bool
	width     int
	height    int
}

func New(h *hub.Hub, cfg *config.Config, initialHost, initialPass string) Model {
	host := textinput.New()
	host.Placeholder = "host[:port]"
	host.CharLimit = 253
	host.Width = 40
	host.Prompt = "▸ "
	if initialHost != "" {
		host.SetValue(initialHost)
	} else if len(cfg.RecentHosts) > 0 {
		host.SetValue(cfg.RecentHosts[0])
	}
	host.Focus()

	pass := textinput.New()
	pass.Placeholder = "password (empty = none)"
	pass.CharLimit = 128
	pass.Width = 40
	pass.Prompt = "▸ "
	pass.EchoMode = textinput.EchoPassword
	pass.EchoCharacter = '•'
	if initialPass != "" {
		pass.SetValue(initialPass)
	}

	m := Model{
		h:         h,
		cfg:       cfg,
		screen:    screenConnect,
		styles:    makeStyles(),
		hostInput: host,
		passInput: pass,
		status:    h.Status(),
	}
	m.applyInputStyles()
	return m
}

func (m *Model) applyInputStyles() {
	t := theme.Current()
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Hex(t.Accent)))
	fg := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Hex(t.Fg)))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Hex(t.FgDim)))
	faint := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Hex(t.FgFaint)))
	if m.focus == 0 {
		m.hostInput.PromptStyle, m.hostInput.TextStyle = accent, fg
		m.passInput.PromptStyle, m.passInput.TextStyle = dim, dim
	} else {
		m.hostInput.PromptStyle, m.hostInput.TextStyle = dim, dim
		m.passInput.PromptStyle, m.passInput.TextStyle = accent, fg
	}
	m.hostInput.PlaceholderStyle = faint
	m.passInput.PlaceholderStyle = faint
}

type themeMsg struct{}

func ThemeMsg() tea.Msg { return themeMsg{} }

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case themeMsg:
		m.styles = makeStyles()
		m.applyInputStyles()
		return m, nil

	case hub.StatusMsg:
		m.status = hub.Status(msg)
		if m.status.Connected {
			m.screen = screenSession
		} else if m.screen == screenSession && !m.status.Busy {
			m.screen = screenConnect
			m.hostInput.Focus()
			m.passInput.Blur()
			m.focus = 0
		}
		return m, nil

	case tea.KeyMsg:
		if m.quitting {
			return m, nil
		}
		switch m.screen {
		case screenConnect:
			return m.updateConnect(msg)
		case screenSession:
			return m.updateSession(msg)
		}
	}

	if m.screen == screenConnect {
		var cmd tea.Cmd
		if m.focus == 0 {
			m.hostInput, cmd = m.hostInput.Update(msg)
		} else {
			m.passInput, cmd = m.passInput.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m Model) updateConnect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if !m.hostInput.Focused() && !m.passInput.Focused() {
			m.quitting = true
			m.h.Shutdown()
			return m, tea.Quit
		}
		// when focused, q types into the field unless ctrl+c
		if msg.String() == "ctrl+c" {
			m.quitting = true
			m.h.Shutdown()
			return m, tea.Quit
		}
	case "esc":
		m.quitting = true
		m.h.Shutdown()
		return m, tea.Quit
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
		if m.focus == 0 {
			m.hostInput.Focus()
			m.passInput.Blur()
		} else {
			m.hostInput.Blur()
			m.passInput.Focus()
		}
		m.applyInputStyles()
		return m, textinput.Blink
	case "enter":
		if m.status.Busy {
			return m, nil
		}
		host := strings.TrimSpace(m.hostInput.Value())
		if host == "" {
			m.status.Message = "host required"
			return m, nil
		}
		m.h.Connect(host, m.passInput.Value())
		return m, nil
	}

	var cmd tea.Cmd
	if m.focus == 0 {
		m.hostInput, cmd = m.hostInput.Update(msg)
	} else {
		m.passInput, cmd = m.passInput.Update(msg)
	}
	// Re-check quit on ctrl+c after textinput ate keys
	if msg.String() == "ctrl+c" {
		m.quitting = true
		m.h.Shutdown()
		return m, tea.Quit
	}
	return m, cmd
}

func (m Model) updateSession(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		m.quitting = true
		m.h.Shutdown()
		return m, tea.Quit
	case "d":
		m.h.Disconnect()
		return m, nil
	case "s":
		m.h.CycleScale()
		return m, nil
	case "r":
		m.h.Refresh()
		return m, nil
	case "c":
		m.h.PushClipboardNow()
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	switch m.screen {
	case screenSession:
		return m.viewSession()
	default:
		return m.viewConnect()
	}
}

func (m Model) viewConnect() string {
	s := m.styles
	var b strings.Builder
	b.WriteString(s.title.Render("omavnc") + "\n")
	b.WriteString(s.dim.Render("VNC viewer — controls in this terminal, remote view in a window") + "\n\n")

	form := s.label.Render("Host") + "\n" + m.hostInput.View() + "\n\n" +
		s.label.Render("Password") + "\n" + m.passInput.View()
	b.WriteString(s.box.Render(form) + "\n\n")

	if len(m.cfg.RecentHosts) > 0 {
		b.WriteString(s.dim.Render("Recent: "+strings.Join(m.cfg.RecentHosts, " · ")) + "\n\n")
	}

	if m.status.Message != "" {
		style := s.dim
		if m.status.Busy {
			style = s.accent
		} else if strings.Contains(m.status.Message, "fail") || strings.Contains(m.status.Message, "error") || strings.Contains(m.status.Message, "dial") {
			style = s.err
		}
		b.WriteString(style.Render(m.status.Message) + "\n\n")
	}

	b.WriteString(s.help.Render("tab fields · enter connect · esc quit"))
	return b.String()
}

func (m Model) viewSession() string {
	s := m.styles
	var b strings.Builder
	b.WriteString(s.title.Render("omavnc") + "  " + s.accent.Render("connected") + "\n\n")

	body := fmt.Sprintf("%s\n%s\n%dx%d  ·  scale %s",
		s.label.Render(m.status.Host),
		s.dim.Render(orDash(m.status.Desktop)),
		m.status.Width, m.status.Height,
		s.accent.Render(m.status.Scale.String()),
	)
	b.WriteString(s.box.Render(body) + "\n\n")

	if m.status.Message != "" {
		b.WriteString(s.dim.Render(m.status.Message) + "\n\n")
	}

	b.WriteString(s.help.Render("s scale · r refresh · c push clipboard · d disconnect · q quit"))
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
