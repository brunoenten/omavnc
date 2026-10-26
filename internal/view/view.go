// Package view is the Gio window that displays the remote framebuffer.
package view

import (
	"image"
	"image/color"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"github.com/brunoenten/omavnc/internal/config"
	"github.com/kward/go-vnc/buttons"
	vnckeys "github.com/kward/go-vnc/keys"
)

// Sender delivers input to the VNC session.
type Sender interface {
	SendKey(k vnckeys.Key, down bool) error
	SendPointer(btn buttons.Button, x, y uint16) error
}

// FrameSource provides the latest remote framebuffer and scale mode.
type FrameSource interface {
	Front() (*image.RGBA, uint64)
	Scale() config.ScaleMode
}

// Window hosts the remote desktop.
type Window struct {
	w       *app.Window
	src     FrameSource
	send    Sender
	onClose func()

	closed atomic.Bool
	tag    int // event.Tag

	imgOp  paint.ImageOp
	imgSeq uint64

	btnMask         buttons.Button
	offX, offY      float32
	scale, fbW, fbH float32
}

// Open creates the view window and runs its event loop in a new goroutine.
func Open(title string, src FrameSource, send Sender, onClose func()) *Window {
	w := new(app.Window)
	w.Option(app.Title(title), app.Size(unit.Dp(1024), unit.Dp(640)))
	v := &Window{w: w, src: src, send: send, onClose: onClose, scale: 1}
	go v.loop()
	return v
}

func (v *Window) Invalidate() {
	if v != nil && v.w != nil && !v.closed.Load() {
		v.w.Invalidate()
	}
}

func (v *Window) SetTitle(title string) {
	if v != nil && v.w != nil {
		v.w.Option(app.Title(title))
	}
}

func (v *Window) Close() {
	if v == nil || v.w == nil {
		return
	}
	v.w.Perform(system.ActionClose)
}

func (v *Window) loop() {
	defer func() {
		v.closed.Store(true)
		if v.onClose != nil {
			v.onClose()
		}
	}()
	var ops op.Ops
	for {
		switch e := v.w.Event().(type) {
		case app.DestroyEvent:
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			v.frame(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (v *Window) frame(gtx layout.Context) {
	tag := &v.tag
	area := clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops)
	event.Op(gtx.Ops, tag)
	area.Pop()
	gtx.Execute(key.FocusCmd{Tag: tag})

	mods := key.ModShift | key.ModCtrl | key.ModAlt | key.ModSuper
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: tag},
			key.Filter{Focus: tag, Optional: mods},
			key.Filter{Focus: tag, Name: key.NameTab, Optional: mods},
			key.Filter{Focus: tag, Name: key.NameEscape, Optional: mods},
			key.Filter{Focus: tag, Name: key.NameReturn, Optional: mods},
			key.Filter{Focus: tag, Name: key.NameEnter, Optional: mods},
			pointer.Filter{Target: tag, Kinds: pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll},
		)
		if !ok {
			break
		}
		switch ev := ev.(type) {
		case key.Event:
			if k, ok := mapKey(ev.Name); ok {
				_ = v.send.SendKey(k, ev.State == key.Press)
			}
		case pointer.Event:
			v.onPointer(ev)
		}
	}

	size := gtx.Constraints.Max
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xff})

	fb, seq := v.src.Front()
	if fb == nil {
		return
	}
	if seq != v.imgSeq {
		// NewImageOp caches by handle; recreate only when the published
		// buffer changes so we don't re-upload every redraw.
		v.imgOp = paint.NewImageOp(fb)
		v.imgSeq = seq
	}

	fbBounds := fb.Bounds()
	fbW, fbH := float32(fbBounds.Dx()), float32(fbBounds.Dy())
	if fbW < 1 || fbH < 1 {
		return
	}
	winW, winH := float32(size.X), float32(size.Y)

	var scale float32
	switch v.src.Scale() {
	case config.Scale1x:
		scale = 1
	case config.Scale2x:
		scale = 2
	default:
		sx, sy := winW/fbW, winH/fbH
		scale = sx
		if sy < sx {
			scale = sy
		}
		if scale <= 0 {
			scale = 1
		}
	}
	drawW, drawH := fbW*scale, fbH*scale
	offX := (winW - drawW) / 2
	offY := (winH - drawH) / 2
	if offX < 0 {
		offX = 0
	}
	if offY < 0 {
		offY = 0
	}
	v.offX, v.offY, v.scale, v.fbW, v.fbH = offX, offY, scale, fbW, fbH

	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	aff := f32.AffineId().Scale(f32.Pt(0, 0), f32.Pt(scale, scale)).Offset(f32.Pt(offX, offY))
	defer op.Affine(aff).Push(gtx.Ops).Pop()
	v.imgOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

func (v *Window) onPointer(ev pointer.Event) {
	x := (ev.Position.X - v.offX) / v.scale
	y := (ev.Position.Y - v.offY) / v.scale
	if v.scale == 0 {
		return
	}
	ux := uint16(clamp(x, 0, v.fbW-1))
	uy := uint16(clamp(y, 0, v.fbH-1))

	switch ev.Kind {
	case pointer.Scroll:
		btn := buttons.Four
		if ev.Scroll.Y > 0 {
			btn = buttons.Five
		}
		_ = v.send.SendPointer(v.btnMask|btn, ux, uy)
		_ = v.send.SendPointer(v.btnMask, ux, uy)
	default:
		mask := buttons.None
		if ev.Buttons.Contain(pointer.ButtonPrimary) {
			mask |= buttons.Left
		}
		if ev.Buttons.Contain(pointer.ButtonSecondary) {
			mask |= buttons.Right
		}
		if ev.Buttons.Contain(pointer.ButtonTertiary) {
			mask |= buttons.Middle
		}
		v.btnMask = mask
		_ = v.send.SendPointer(mask, ux, uy)
	}
}

func clamp(v, lo, hi float32) float32 {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
