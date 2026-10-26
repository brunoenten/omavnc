// Package hub coordinates the TUI, VNC session, view window, and clipboard.
package hub

import (
	"context"
	"fmt"
	"image"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/kward/go-vnc/buttons"
	vnckeys "github.com/kward/go-vnc/keys"

	"github.com/brunoenten/omavnc/internal/clipboard"
	"github.com/brunoenten/omavnc/internal/config"
	"github.com/brunoenten/omavnc/internal/view"
	vncclient "github.com/brunoenten/omavnc/internal/vnc"
)

// Status is a snapshot for the TUI.
type Status struct {
	Connected bool
	Host      string
	Desktop   string
	Width     int
	Height    int
	Scale     config.ScaleMode
	Message   string // transient note / error
	Busy      bool
}

// StatusMsg is a bubbletea message carrying Status.
type StatusMsg Status

// Hub owns session state shared by the TUI and view.
type Hub struct {
	cfg    *config.Config
	teaProg *tea.Program

	mu         sync.Mutex
	client     *vncclient.Client
	win        *view.Window
	scale      config.ScaleMode
	status     Status
	clip       clipboard.Watcher
	frameN     atomic.Uint64
	closing    atomic.Bool
	lastRedraw atomic.Int64 // unix nano; coalesce Invalidate to ~30 Hz
}

func New(cfg *config.Config) *Hub {
	h := &Hub{cfg: cfg, scale: cfg.DefaultScale}
	h.status.Scale = h.scale
	return h
}

func (h *Hub) SetProgram(p *tea.Program) { h.teaProg = p }

func (h *Hub) notify() {
	if h.teaProg == nil {
		return
	}
	h.mu.Lock()
	s := h.status
	s.Scale = h.scale
	h.mu.Unlock()
	h.teaProg.Send(StatusMsg(s))
}

func (h *Hub) setMsg(msg string) {
	h.mu.Lock()
	h.status.Message = msg
	h.mu.Unlock()
	h.notify()
}

// Scale implements view.FrameSource.
func (h *Hub) Scale() config.ScaleMode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.scale
}

// Front implements view.FrameSource.
func (h *Hub) Front() (*image.RGBA, uint64) {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil {
		return nil, 0
	}
	return c.Front()
}

func (h *Hub) SendKey(k vnckeys.Key, down bool) error {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	return c.Key(k, down)
}

func (h *Hub) SendPointer(btn buttons.Button, x, y uint16) error {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	return c.Pointer(btn, x, y)
}

// Connect dials the VNC server and opens the view window.
func (h *Hub) Connect(host, password string) {
	h.mu.Lock()
	if h.status.Busy || h.client != nil {
		h.mu.Unlock()
		return
	}
	h.status.Busy = true
	h.status.Message = "connecting…"
	h.mu.Unlock()
	h.notify()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), vncclient.DialTimeout())
		defer cancel()

		var client *vncclient.Client
		opt := vncclient.Options{
			Host:     host,
			Password: password,
			OnFrame: func() {
				h.frameN.Add(1)
				// Coalesce redraws so the Gio loop isn't flooded (and can
				// keep answering xdg_wm_base pings between frames).
				now := time.Now().UnixNano()
				prev := h.lastRedraw.Load()
				if now-prev < int64(33*time.Millisecond) {
					return
				}
				if !h.lastRedraw.CompareAndSwap(prev, now) {
					return
				}
				h.mu.Lock()
				w := h.win
				h.mu.Unlock()
				if w != nil {
					w.Invalidate()
				}
			},
			OnCut: func(text string) {
				h.clip.SetLast(text)
				if err := clipboard.Copy(text); err != nil {
					h.setMsg("remote clipboard: " + err.Error())
				} else {
					h.setMsg("received remote clipboard")
				}
			},
			OnClose: func(err error) {
				h.mu.Lock()
				if err != nil {
					h.status.Message = "disconnected: " + err.Error()
				}
				h.mu.Unlock()
				h.Disconnect()
			},
			OnInfo: func(name string, w, hgt int) {
				h.mu.Lock()
				h.status.Desktop = name
				h.status.Width = w
				h.status.Height = hgt
				win := h.win
				h.mu.Unlock()
				if win != nil {
					title := name
					if title == "" {
						title = host
					}
					win.SetTitle("omavnc — " + title)
				}
				h.notify()
			},
		}

		var err error
		client, err = vncclient.Connect(ctx, opt)
		if err != nil {
			h.mu.Lock()
			h.status.Busy = false
			h.status.Message = err.Error()
			h.mu.Unlock()
			h.notify()
			return
		}

		h.mu.Lock()
		h.client = client
		h.status.Busy = false
		h.status.Connected = true
		h.status.Host = client.Host()
		h.status.Desktop = client.Name()
		h.status.Width, h.status.Height = client.Size()
		h.status.Message = "connected"
		h.mu.Unlock()

		h.cfg.RememberHost(host)
		h.startClipboard()

		title := client.Name()
		if title == "" {
			title = client.Host()
		}
		win := view.Open("omavnc — "+title, h, h, func() {
			h.Disconnect()
		})
		h.mu.Lock()
		h.win = win
		h.mu.Unlock()
		h.notify()
	}()
}

func (h *Hub) startClipboard() {
	_ = h.clip.Start(func(text string) {
		h.pushClipboard(text)
	})
}

func (h *Hub) pushClipboard(text string) {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil {
		return
	}
	cleaned, err := c.CutText(text)
	if err != nil && cleaned == "" {
		h.setMsg("clipboard: " + err.Error())
		return
	}
	h.clip.SetLast(text)
	if err != nil {
		h.setMsg("sent clipboard (" + err.Error() + ")")
	} else {
		h.setMsg("sent clipboard to remote")
	}
}

// PushClipboardNow reads local clipboard and sends it.
func (h *Hub) PushClipboardNow() {
	text, err := clipboard.Paste()
	if err != nil {
		h.setMsg("clipboard: " + err.Error())
		return
	}
	h.pushClipboard(text)
}

func (h *Hub) CycleScale() {
	h.mu.Lock()
	h.scale = h.scale.Next()
	h.status.Scale = h.scale
	h.cfg.DefaultScale = h.scale
	win := h.win
	h.mu.Unlock()
	_ = h.cfg.Save()
	if win != nil {
		win.Invalidate()
	}
	h.setMsg("scale " + h.Scale().String())
}

func (h *Hub) Refresh() {
	h.mu.Lock()
	c := h.client
	h.mu.Unlock()
	if c == nil {
		return
	}
	if err := c.Refresh(); err != nil {
		h.setMsg(err.Error())
	} else {
		h.setMsg("refresh requested")
	}
}

func (h *Hub) Disconnect() {
	if !h.closing.CompareAndSwap(false, true) {
		return
	}
	defer h.closing.Store(false)

	h.mu.Lock()
	c := h.client
	w := h.win
	h.client = nil
	h.win = nil
	h.mu.Unlock()
	h.clip.Stop()
	if w != nil {
		w.Close()
	}
	if c != nil {
		c.Close()
	}
	h.handleDisconnect(nil)
}

func (h *Hub) handleDisconnect(err error) {
	h.mu.Lock()
	was := h.status.Connected || h.client != nil
	h.client = nil
	h.win = nil
	h.status.Connected = false
	h.status.Busy = false
	h.status.Desktop = ""
	h.status.Width, h.status.Height = 0, 0
	if err != nil {
		h.status.Message = "disconnected: " + err.Error()
	} else if was && !strings.HasPrefix(h.status.Message, "disconnected") {
		h.status.Message = "disconnected"
	}
	h.mu.Unlock()
	h.clip.Stop()
	h.notify()
}

// Shutdown tears everything down.
func (h *Hub) Shutdown() {
	h.Disconnect()
	// Give the view a moment to close.
	time.Sleep(50 * time.Millisecond)
}

// Status returns the current status snapshot.
func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.status
	s.Scale = h.scale
	return s
}
