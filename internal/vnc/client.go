// Package vnc is a minimal RFB 3.8 client (Raw encoding, None/VNC-auth).
package vnc

import (
	"bytes"
	"context"
	"crypto/des"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/kward/go-vnc/buttons"
	"github.com/kward/go-vnc/keys"
)

// Client is a live VNC session with an RGBA framebuffer.
//
// The reader path (Front) never blocks on network or blit work: the decode
// goroutine mutates a private work buffer, then publishes an immutable copy
// via front/seq. That keeps the Gio event loop free to answer Wayland pings.
type Client struct {
	mu     sync.Mutex
	conn   net.Conn
	work   *image.RGBA // owned by the reader goroutine; publish clones it
	front  atomic.Pointer[image.RGBA]
	seq    atomic.Uint64
	host   string
	name   string
	width  uint16
	height uint16
	closed chan struct{}

	onFrame   func()
	onCutText func(string)
	onClose   func(error)
	onDesktop func(name string, w, h int)
}

type Options struct {
	Host     string
	Password string
	OnFrame  func()
	OnCut    func(string)
	OnClose  func(error)
	OnInfo   func(name string, w, h int)
}

// Connect dials and handshakes with the VNC server.
func Connect(ctx context.Context, opt Options) (*Client, error) {
	addr := normalizeAddr(opt.Host)
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = nc.SetDeadline(deadline)
	}

	c := &Client{
		conn:      nc,
		host:      addr,
		closed:    make(chan struct{}),
		onFrame:   opt.OnFrame,
		onCutText: opt.OnCut,
		onClose:   opt.OnClose,
		onDesktop: opt.OnInfo,
	}
	if err := c.handshake(opt.Password); err != nil {
		nc.Close()
		return nil, err
	}
	_ = nc.SetDeadline(time.Time{})

	c.work = image.NewRGBA(image.Rect(0, 0, int(c.width), int(c.height)))
	c.publishLocked()
	if c.onDesktop != nil {
		c.onDesktop(c.name, int(c.width), int(c.height))
	}

	go c.readLoop()
	if err := c.requestUpdate(false); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func normalizeAddr(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return "127.0.0.1:5900"
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if strings.Count(host, ":") == 0 {
		return net.JoinHostPort(host, "5900")
	}
	return host
}

func (c *Client) Host() string { return c.host }
func (c *Client) Name() string { return c.name }

func (c *Client) Size() (int, int) {
	if fb := c.front.Load(); fb != nil {
		b := fb.Bounds()
		return b.Dx(), b.Dy()
	}
	return int(c.width), int(c.height)
}

// Front returns the latest published framebuffer (read-only) and its sequence.
// Safe to call from the UI goroutine; never blocks on decode.
func (c *Client) Front() (*image.RGBA, uint64) {
	return c.front.Load(), c.seq.Load()
}

// publishLocked clones work into front. Caller must hold c.mu (or be at init).
func (c *Client) publishLocked() {
	if c.work == nil {
		return
	}
	dst := image.NewRGBA(c.work.Bounds())
	copy(dst.Pix, c.work.Pix)
	c.front.Store(dst)
	c.seq.Add(1)
}

func (c *Client) publish() {
	c.mu.Lock()
	c.publishLocked()
	c.mu.Unlock()
}

func (c *Client) Close() {
	select {
	case <-c.closed:
		return
	default:
		close(c.closed)
	}
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *Client) requestUpdate(incremental bool) error {
	c.mu.Lock()
	conn := c.conn
	w, h := c.width, c.height
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	inc := byte(0)
	if incremental {
		inc = 1
	}
	var buf [10]byte
	buf[0] = 3
	buf[1] = inc
	binary.BigEndian.PutUint16(buf[2:], 0)
	binary.BigEndian.PutUint16(buf[4:], 0)
	binary.BigEndian.PutUint16(buf[6:], w)
	binary.BigEndian.PutUint16(buf[8:], h)
	_, err := conn.Write(buf[:])
	return err
}

func (c *Client) Refresh() error { return c.requestUpdate(false) }

func (c *Client) Pointer(button buttons.Button, x, y uint16) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	var buf [6]byte
	buf[0] = 5
	buf[1] = uint8(button)
	binary.BigEndian.PutUint16(buf[2:], x)
	binary.BigEndian.PutUint16(buf[4:], y)
	_, err := conn.Write(buf[:])
	return err
}

func (c *Client) Key(key keys.Key, down bool) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected")
	}
	var buf [8]byte
	buf[0] = 4
	if down {
		buf[1] = 1
	}
	binary.BigEndian.PutUint32(buf[4:], uint32(key))
	_, err := conn.Write(buf[:])
	return err
}

func (c *Client) CutText(text string) (string, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return "", fmt.Errorf("not connected")
	}
	cleaned, truncated := toLatin1(text)
	if cleaned == "" {
		return "", fmt.Errorf("clipboard empty after Latin-1 filter")
	}
	var hdr [8]byte
	hdr[0] = 6
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(cleaned)))
	if _, err := conn.Write(hdr[:]); err != nil {
		return cleaned, err
	}
	if _, err := io.WriteString(conn, cleaned); err != nil {
		return cleaned, err
	}
	if truncated {
		return cleaned, fmt.Errorf("truncated non-Latin-1 characters")
	}
	return cleaned, nil
}

func toLatin1(s string) (string, bool) {
	var b strings.Builder
	truncated := false
	for _, r := range s {
		if r == '\r' {
			continue
		}
		if r > unicode.MaxLatin1 {
			truncated = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), truncated
}

func (c *Client) handshake(password string) error {
	var ver [12]byte
	if _, err := io.ReadFull(c.conn, ver[:]); err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	if !bytes.HasPrefix(ver[:], []byte("RFB ")) {
		return fmt.Errorf("bad version greeting %q", ver)
	}
	if _, err := c.conn.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}

	var nAuth [1]byte
	if _, err := io.ReadFull(c.conn, nAuth[:]); err != nil {
		return err
	}
	if nAuth[0] == 0 {
		var reasonLen uint32
		if err := binary.Read(c.conn, binary.BigEndian, &reasonLen); err != nil {
			return err
		}
		reason := make([]byte, reasonLen)
		_, _ = io.ReadFull(c.conn, reason)
		return fmt.Errorf("server rejected connection: %s", reason)
	}
	auths := make([]byte, nAuth[0])
	if _, err := io.ReadFull(c.conn, auths); err != nil {
		return err
	}
	want := byte(1) // None
	if password != "" {
		want = 2 // VNC auth
	}
	chosen := byte(0)
	for _, a := range auths {
		if a == want {
			chosen = a
			break
		}
	}
	if chosen == 0 {
		for _, a := range auths {
			if a == 1 || a == 2 {
				chosen = a
				break
			}
		}
	}
	if chosen == 0 {
		return fmt.Errorf("no supported security type in %v", auths)
	}
	if _, err := c.conn.Write([]byte{chosen}); err != nil {
		return err
	}
	if chosen == 2 {
		if err := c.vncAuth(password); err != nil {
			return err
		}
	}
	var result uint32
	if err := binary.Read(c.conn, binary.BigEndian, &result); err != nil {
		return fmt.Errorf("security result: %w", err)
	}
	if result != 0 {
		var reasonLen uint32
		_ = binary.Read(c.conn, binary.BigEndian, &reasonLen)
		reason := make([]byte, reasonLen)
		_, _ = io.ReadFull(c.conn, reason)
		return fmt.Errorf("authentication failed: %s", reason)
	}

	// ClientInit: shared
	if _, err := c.conn.Write([]byte{1}); err != nil {
		return err
	}

	var init [24]byte
	if _, err := io.ReadFull(c.conn, init[:]); err != nil {
		return fmt.Errorf("server init: %w", err)
	}
	c.width = binary.BigEndian.Uint16(init[0:])
	c.height = binary.BigEndian.Uint16(init[2:])
	nameLen := binary.BigEndian.Uint32(init[20:])
	name := make([]byte, nameLen)
	if _, err := io.ReadFull(c.conn, name); err != nil {
		return fmt.Errorf("desktop name: %w", err)
	}
	c.name = string(name)

	// SetPixelFormat: 32bpp little-endian truecolor (R@16 G@8 B@0).
	pf := make([]byte, 20)
	pf[0] = 0 // SetPixelFormat
	pf[4] = 32
	pf[5] = 24
	pf[6] = 0 // little-endian
	pf[7] = 1 // true-color
	binary.BigEndian.PutUint16(pf[8:], 255)
	binary.BigEndian.PutUint16(pf[10:], 255)
	binary.BigEndian.PutUint16(pf[12:], 255)
	pf[14], pf[15], pf[16] = 16, 8, 0
	if _, err := c.conn.Write(pf); err != nil {
		return err
	}

	// SetEncodings: Raw only
	enc := []byte{2, 0, 0, 1, 0, 0, 0, 0}
	if _, err := c.conn.Write(enc); err != nil {
		return err
	}
	return nil
}

func (c *Client) vncAuth(password string) error {
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(c.conn, challenge); err != nil {
		return err
	}
	key := make([]byte, 8)
	copy(key, []byte(password))
	// VNC DES key bit-flip
	for i := range key {
		key[i] = reverseBits(key[i])
	}
	block, err := des.NewCipher(key)
	if err != nil {
		return err
	}
	resp := make([]byte, 16)
	block.Encrypt(resp[:8], challenge[:8])
	block.Encrypt(resp[8:], challenge[8:])
	_, err = c.conn.Write(resp)
	return err
}

func reverseBits(b byte) byte {
	var r byte
	for i := 0; i < 8; i++ {
		r <<= 1
		r |= b & 1
		b >>= 1
	}
	return r
}

func (c *Client) readLoop() {
	var err error
	defer func() {
		c.Close()
		if c.onClose != nil {
			c.onClose(err)
		}
	}()
	for {
		select {
		case <-c.closed:
			err = nil
			return
		default:
		}
		var msgType [1]byte
		if _, err = io.ReadFull(c.conn, msgType[:]); err != nil {
			return
		}
		switch msgType[0] {
		case 0: // FramebufferUpdate
			if err = c.readFramebufferUpdate(); err != nil {
				return
			}
			_ = c.requestUpdate(true)
			if c.onFrame != nil {
				c.onFrame() // after publish; UI only loads the atomic front buffer
			}
		case 1: // SetColourMapEntries
			if err = c.discardColorMap(); err != nil {
				return
			}
		case 2: // Bell
			// nothing
		case 3: // ServerCutText
			text, e := c.readCutText()
			if e != nil {
				err = e
				return
			}
			if c.onCutText != nil && text != "" {
				c.onCutText(text)
			}
		default:
			err = fmt.Errorf("unknown server message %d", msgType[0])
			return
		}
	}
}

func (c *Client) readFramebufferUpdate() error {
	var pad [1]byte
	if _, err := io.ReadFull(c.conn, pad[:]); err != nil {
		return err
	}
	var nRect uint16
	if err := binary.Read(c.conn, binary.BigEndian, &nRect); err != nil {
		return err
	}
	for i := 0; i < int(nRect); i++ {
		var hdr [12]byte
		if _, err := io.ReadFull(c.conn, hdr[:]); err != nil {
			return err
		}
		x := binary.BigEndian.Uint16(hdr[0:])
		y := binary.BigEndian.Uint16(hdr[2:])
		w := binary.BigEndian.Uint16(hdr[4:])
		h := binary.BigEndian.Uint16(hdr[6:])
		enc := int32(binary.BigEndian.Uint32(hdr[8:]))
		switch enc {
		case 0: // Raw
			if err := c.readRaw(x, y, w, h); err != nil {
				return err
			}
		case -223: // DesktopSize
			c.mu.Lock()
			c.width, c.height = w, h
			c.work = image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
			c.publishLocked()
			c.mu.Unlock()
			if c.onDesktop != nil {
				c.onDesktop(c.name, int(w), int(h))
			}
		default:
			return fmt.Errorf("unsupported encoding %d", enc)
		}
	}
	// Publish once per update so the UI never waits on blit/network.
	c.publish()
	return nil
}

func (c *Client) readRaw(x, y, w, h uint16) error {
	n := int(w) * int(h) * 4
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.conn, buf); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fb := c.work
	if fb == nil {
		return nil
	}
	bounds := fb.Bounds()
	xw, yw := int(x), int(y)
	ww, hh := int(w), int(h)
	for row := 0; row < hh; row++ {
		py := yw + row
		if py < bounds.Min.Y || py >= bounds.Max.Y {
			continue
		}
		src := buf[row*ww*4:]
		dstOff := py*fb.Stride + xw*4
		for col := 0; col < ww; col++ {
			px := xw + col
			if px < bounds.Min.X || px >= bounds.Max.X {
				continue
			}
			si := col * 4
			di := dstOff + col*4
			// LE 32bpp R@16 G@8 B@0 → bytes B,G,R,X → Gio RGBA
			fb.Pix[di+0] = src[si+2]
			fb.Pix[di+1] = src[si+1]
			fb.Pix[di+2] = src[si+0]
			fb.Pix[di+3] = 255
		}
	}
	return nil
}

func (c *Client) discardColorMap() error {
	var pad [1]byte
	if _, err := io.ReadFull(c.conn, pad[:]); err != nil {
		return err
	}
	var first, n uint16
	if err := binary.Read(c.conn, binary.BigEndian, &first); err != nil {
		return err
	}
	if err := binary.Read(c.conn, binary.BigEndian, &n); err != nil {
		return err
	}
	_, err := io.CopyN(io.Discard, c.conn, int64(n)*6)
	return err
}

func (c *Client) readCutText() (string, error) {
	var pad [3]byte
	if _, err := io.ReadFull(c.conn, pad[:]); err != nil {
		return "", err
	}
	var n uint32
	if err := binary.Read(c.conn, binary.BigEndian, &n); err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c.conn, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func DialTimeout() time.Duration { return 15 * time.Second }
