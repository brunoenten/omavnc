# omavnc

A VNC viewer for [Omarchy](https://omarchy.org): controls live in the terminal,
the remote desktop opens in a dedicated window.

```
omavnc
VNC viewer — controls in this terminal, remote view in a window

┌ Host ─────────────────────────┐
│ ▸ myserver:5900               │
│                               │
│ Password                      │
│ ▸ ••••••                      │
└───────────────────────────────┘

tab fields · enter connect · esc quit
```

## Features

- Terminal TUI (Charm bubbletea) for connect, scale, clipboard, disconnect
- Gio Wayland window for the remote framebuffer and keyboard/mouse input
- Scale modes: `fit`, `1:1`, `2x` (cycle with `s`)
- Local → remote clipboard via `wl-paste --watch` and RFB `ClientCutText`
- Remote → local clipboard best-effort via `wl-copy`
- Colors follow `omarchy theme set` live (`colors.toml`)

## Install

Requires Go 1.27+, Wayland, and `wl-clipboard`.

```sh
cd omavnc
make install   # ~/.local/bin/omavnc
```

`make run` builds and starts without installing.

## Usage

```sh
omavnc                         # connect form
omavnc host[:port]             # default port 5900
omavnc -password secret host
OMAVNC_PASSWORD=secret omavnc host
```

### Session keys

| Key | Action              |
|-----|---------------------|
| `s` | Cycle scale         |
| `r` | Full framebuffer refresh |
| `c` | Push local clipboard to remote |
| `d` | Disconnect          |
| `q` | Quit                |

## Configuration

Optional `~/.config/omavnc/config.toml`:

```toml
default_scale = "fit"   # fit | 1:1 | 2x
recent_hosts = ["myserver:5900"]
```

## Notes

- RFB 3.8 client is built-in (security: None or VNC password; encoding: Raw TrueColor 32bpp). Fine on a LAN; WAN will be heavy.
- Clipboard text is Latin-1 only (RFB §7.5.6); other characters are dropped.
