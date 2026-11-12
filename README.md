# bandwave

A cava-style terminal audio visualizer that also shows what's playing on the
**other computers around you**, on Linux and Windows.

```
                                   ▅▅    ▃▃
                             ▂▂    ██ ▆▆ ██          ▁▁
                    ▄▄ ▇▇    ██ ▅▅ ██ ██ ██ ▃▃       ██
              ▃▃ ▆▆ ██ ██ ▅▅ ██ ██ ██ ██ ██ ██ ▆▆ ▂▂ ██ ▄▄
 studio-pc · auto · 2 source(s) · tab next · a auto · q quit
```

## Why

cava only sees audio that plays on the machine it runs on. KDE Connect makes it
feel like your devices are one system (media controls, notifications), but it
never carries the audio itself, so when music plays on another PC, cava sits
flat. No cava setting can fix that.

bandwave runs a small **agent** on each PC. It captures what that PC is
playing (PipeWire/PulseAudio monitor on Linux, WASAPI loopback on Windows),
runs the FFT locally and streams only the bar heights (96 numbers per frame,
about 23 KB/s while playing, almost nothing while silent). The **viewer**
finds agents through KDE Connect's device list and LAN broadcasts, and draws
whichever machine is playing right now.

No audio ever leaves a machine, only the spectrum.

## Install

Download a binary from [Releases](https://github.com/KigoJomo/bandwave/releases),
or build it (Go 1.26+, no cgo):

```bash
go install github.com/KigoJomo/bandwave@latest
```

Cross-compile Windows from Linux with `make windows`.

Linux capture needs `parec` (libpulse, present on PipeWire systems with
`pipewire-pulse`) or `pw-record`.

## Use

On each machine whose audio you want to see, start the agent at login:

```bash
bandwave install
```

- **Linux:** writes and starts a systemd user unit (`~/.config/systemd/user/bandwave.service`).
- **Windows:** creates a logon task running `bandwave-agent.exe` (a build with no
  console window) and, from an elevated prompt, adds a Windows Firewall rule
  for private/domain networks. Logs go to `%LOCALAPPDATA%\bandwave\agent.log`.

Then just run the viewer wherever you want the bars:

```bash
bandwave
```

It shows this PC's own audio too, so it replaces cava. In **auto** mode it
follows whichever source is playing, and stays on it until it goes quiet.

| Key | Action |
|---|---|
| `tab` / `n` | next source (pins the view) |
| `p` | previous source |
| `a` | back to auto-follow |
| `s` | toggle the status line |
| `q` / `esc` / `ctrl-c` | quit |

`bandwave list -v` shows which agents it can reach, and why the others failed.

### Options

```
bandwave [--peer host[:port]] [--no-local] [--source name] [--colors '#hex,#hex,...']
         [--bar-width 2] [--gap 1] [--no-status] [--no-lan] [--no-kdeconnect] [--device name]
bandwave serve [--listen :47630] [--no-beacon] [--device name] [--log file]
```

Colors: with no `--colors`, bandwave reads the `[color]` section (and `theme`)
of your existing `~/.config/cava/config`, so your gradient carries over. With
no cava colors either, it uses the terminal's own palette, which follows your
terminal theme.

## Firewalls

The viewer connects to agents on **TCP 47630**; agents announce themselves on
**UDP 47631** broadcast. The machine running the *agent* must accept TCP 47630,
and the *viewer* must accept UDP 47631 for broadcast discovery (KDE Connect
discovery and `--peer` don't need it).

```bash
# Linux with ufw (replace the subnet with your LAN's)
sudo ufw allow from 10.0.0.0/8 to any port 47630 proto tcp
sudo ufw allow from 10.0.0.0/8 to any port 47631 proto udp
```

On Windows, run `bandwave install` from an elevated prompt once, or add the
rule it prints.

Agents listen on all interfaces and serve spectrum data to anyone who can
reach the port. That is not audio, but it does reveal *when* a machine is
playing something. Use `--listen 10.0.0.5:47630` or your firewall to limit it.

## Limits

- **Phones** aren't sources. Android only lets apps capture other apps' audio
  through a screen-capture prompt, so there's no quiet agent for it.
- On Windows, the agent follows the default output device; switching it
  restarts capture within a second.
- Agents and viewers must be the same protocol version (the handshake checks).

## How it works

```
 agent (each PC)                                viewer
 ─────────────────                              ──────────────────────────
 parec / WASAPI loopback                        local capture ─┐
   → mono float32, 48 kHz                                       ├→ pick source → autosens,
   → 4096-point Hann FFT @ 60 fps                agents (TCP) ──┘   gravity, resample
   → 96 log bands, 40 Hz-16 kHz                  ▲                  to terminal width → ANSI
   → TCP to each viewer ─────────────────────────┘
 UDP beacon every 2 s ─────────────→ discovery ← kdeconnect-cli -l
```

Protocol: the client sends `BANDWAVE/1\n`, the agent answers with one JSON line
(`name`, `os`, `version`, `bands`), then length-prefixed frames of
`float32 level + 96 × float32 bands`.

## License

MIT
