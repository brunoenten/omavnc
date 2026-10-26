// Package clipboard bridges the local Wayland clipboard with VNC cut-text.
package clipboard

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// Watcher observes local clipboard changes via `wl-paste --watch`.
type Watcher struct {
	mu     sync.Mutex
	last   string
	cancel context.CancelFunc
}

// Start watches the clipboard and calls onText for each new value.
// Echo suppression: SetLast should be called when we write remote→local so we
// do not bounce the same text back to the server.
func (w *Watcher) Start(onText func(string)) error {
	if _, err := exec.LookPath("wl-paste"); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
	}
	w.cancel = cancel
	w.mu.Unlock()

	cmd := exec.CommandContext(ctx, "wl-paste", "--watch", "echo")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			text, err := Paste()
			if err != nil || text == "" {
				continue
			}
			w.mu.Lock()
			if text == w.last {
				w.mu.Unlock()
				continue
			}
			w.last = text
			w.mu.Unlock()
			onText(text)
		}
		_ = cmd.Wait()
	}()
	return nil
}

func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
}

func (w *Watcher) SetLast(text string) {
	w.mu.Lock()
	w.last = text
	w.mu.Unlock()
}

// Paste returns the current clipboard text (no newline trimming beyond wl-paste -n).
func Paste() (string, error) {
	out, err := exec.Command("wl-paste", "-n").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Copy writes text to the clipboard via wl-copy.
func Copy(text string) error {
	if _, err := exec.LookPath("wl-copy"); err != nil {
		return err
	}
	cmd := exec.Command("wl-copy", "--")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, _ = io.WriteString(stdin, text)
	_ = stdin.Close()
	return cmd.Wait()
}

// Latin1Filter drops runes above Latin-1 and strips CRs.
func Latin1Filter(s string) (string, bool) {
	var b strings.Builder
	truncated := false
	for _, r := range s {
		if r == '\r' {
			continue
		}
		if r > 0xff {
			truncated = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), truncated
}
