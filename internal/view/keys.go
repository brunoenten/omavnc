package view

import (
	"unicode/utf8"

	"gioui.org/io/key"
	vnckeys "github.com/kward/go-vnc/keys"
)

func mapKey(name key.Name) (vnckeys.Key, bool) {
	switch name {
	case key.NameReturn, key.NameEnter:
		return vnckeys.Return, true
	case key.NameEscape:
		return vnckeys.Escape, true
	case key.NameDeleteBackward:
		return vnckeys.BackSpace, true
	case key.NameDeleteForward:
		return vnckeys.Delete, true
	case key.NameTab:
		return vnckeys.Tab, true
	case key.NameUpArrow:
		return vnckeys.Up, true
	case key.NameDownArrow:
		return vnckeys.Down, true
	case key.NameLeftArrow:
		return vnckeys.Left, true
	case key.NameRightArrow:
		return vnckeys.Right, true
	case key.NamePageUp:
		return vnckeys.PageUp, true
	case key.NamePageDown:
		return vnckeys.PageDown, true
	case key.NameHome:
		return vnckeys.Home, true
	case key.NameEnd:
		return vnckeys.End, true
	case key.NameF1:
		return vnckeys.F1, true
	case key.NameF2:
		return vnckeys.F2, true
	case key.NameF3:
		return vnckeys.F3, true
	case key.NameF4:
		return vnckeys.F4, true
	case key.NameF5:
		return vnckeys.F5, true
	case key.NameF6:
		return vnckeys.F6, true
	case key.NameF7:
		return vnckeys.F7, true
	case key.NameF8:
		return vnckeys.F8, true
	case key.NameF9:
		return vnckeys.F9, true
	case key.NameF10:
		return vnckeys.F10, true
	case key.NameF11:
		return vnckeys.F11, true
	case key.NameF12:
		return vnckeys.F12, true
	case key.NameCtrl:
		return vnckeys.ControlLeft, true
	case key.NameShift:
		return vnckeys.ShiftLeft, true
	case key.NameAlt:
		return vnckeys.AltLeft, true
	case key.NameSuper:
		return vnckeys.SuperLeft, true
	case key.NameSpace:
		return vnckeys.Space, true
	}
	s := string(name)
	if r, size := utf8.DecodeRuneInString(s); size == len(s) && r != utf8.RuneError {
		return vnckeys.FromRune(r)
	}
	return 0, false
}
