package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type rgb struct{ r, g, b uint8 }

// Style controls how bars look. Colors nil means "use the terminal palette".
type Style struct {
	BarWidth, Gap int
	Colors        []rgb
	Status        bool
}

// Bars turns frames into smoothed, auto-scaled bar heights in 0..1.
type Bars struct {
	sens float64
	val  []float64
	fall []float64
}

func NewBars() *Bars { return &Bars{sens: 40} }

func (b *Bars) Update(f Frame, n int) []float64 {
	if len(b.val) != n {
		b.val, b.fall = make([]float64, n), make([]float64, n)
	}
	raw := resample(f.Bands[:], n)
	top := 0.0
	for i := range raw {
		raw[i] *= b.sens
		top = math.Max(top, raw[i])
	}
	// Autosens: back off quickly when clipping, creep up while audio plays.
	if top > 1 {
		b.sens *= 0.97
	} else if f.Level > silenceLevel {
		if top < 0.4 {
			b.sens *= 1.04 // far from full scale: catch up within a second or two
		} else {
			b.sens *= 1.003
		}
	}
	b.sens = math.Max(1e-3, math.Min(b.sens, 1e5))
	for i, v := range raw {
		v = math.Min(v, 1)
		if v >= b.val[i] {
			b.val[i] = b.val[i]*0.25 + v*0.75
			b.fall[i] = 0
		} else {
			b.fall[i] += 0.0022
			b.val[i] = math.Max(v, b.val[i]-b.fall[i])
		}
	}
	return b.val
}

// resample maps the fixed log-spaced bands onto n bars: max over each bar's
// span when shrinking, linear interpolation when stretching.
func resample(bands []float32, n int) []float64 {
	out := make([]float64, n)
	m := len(bands)
	for i := 0; i < n; i++ {
		lo := float64(i) * float64(m) / float64(n)
		hi := float64(i+1) * float64(m) / float64(n)
		if hi-lo >= 1 {
			v := 0.0
			for j := int(lo); j < int(math.Ceil(hi)) && j < m; j++ {
				v = math.Max(v, float64(bands[j]))
			}
			out[i] = v
		} else {
			c := (lo+hi)/2 - 0.5
			j := int(math.Floor(c))
			t := c - float64(j)
			a, bb := clampIdx(bands, j), clampIdx(bands, j+1)
			out[i] = (1-t)*a + t*bb
		}
	}
	return out
}

func clampIdx(b []float32, i int) float64 {
	if i < 0 {
		i = 0
	}
	if i >= len(b) {
		i = len(b) - 1
	}
	return float64(b[i])
}

var eighths = []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// Draw renders bars into a full-screen ANSI frame of size w x h.
func Draw(sb *strings.Builder, vals []float64, w, h int, st Style, status string) {
	sb.Reset()
	sb.WriteString("\x1b[H")
	rows := h
	if st.Status {
		rows--
	}
	n := len(vals)
	used := n*st.BarWidth + (n-1)*st.Gap
	pad := 0
	if used < w {
		pad = (w - used) / 2
	}
	for row := 0; row < rows; row++ {
		level := rows - row // 1-based from the bottom
		sb.WriteString(rowColor(st.Colors, float64(level-1)/math.Max(1, float64(rows-1))))
		sb.WriteString(strings.Repeat(" ", pad))
		for i, v := range vals {
			cells := v * float64(rows) * 8
			fill := int(cells) - (level-1)*8
			if fill < 0 {
				fill = 0
			}
			if fill > 8 {
				fill = 8
			}
			ch := eighths[fill]
			for k := 0; k < st.BarWidth; k++ {
				sb.WriteString(ch)
			}
			if i < n-1 {
				sb.WriteString(strings.Repeat(" ", st.Gap))
			}
		}
		sb.WriteString("\x1b[K")
		if row < rows-1 || st.Status {
			sb.WriteString("\r\n")
		}
	}
	if st.Status {
		sb.WriteString("\x1b[0;2m")
		if r := []rune(status); len(r) > w {
			status = string(r[:max(0, w)])
		}
		sb.WriteString(status)
		sb.WriteString("\x1b[K\x1b[0m")
	}
}

// palette is used when no colors are configured: the terminal's own ANSI
// colors, so it follows whatever theme the terminal has.
var palette = []int{4, 4, 6, 6, 2, 3, 1, 5}

func rowColor(colors []rgb, t float64) string {
	if len(colors) == 0 {
		return fmt.Sprintf("\x1b[0;%dm", 30+palette[min(len(palette)-1, int(t*float64(len(palette))))])
	}
	if len(colors) == 1 {
		c := colors[0]
		return fmt.Sprintf("\x1b[0;38;2;%d;%d;%dm", c.r, c.g, c.b)
	}
	x := t * float64(len(colors)-1)
	i := min(int(x), len(colors)-2)
	f := x - float64(i)
	a, b := colors[i], colors[i+1]
	mix := func(p, q uint8) int { return int(float64(p)*(1-f) + float64(q)*f + 0.5) }
	return fmt.Sprintf("\x1b[0;38;2;%d;%d;%dm", mix(a.r, b.r), mix(a.g, b.g), mix(a.b, b.b))
}

func parseHex(s string) (rgb, bool) {
	s = strings.Trim(strings.TrimSpace(s), `'"`)
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return rgb{}, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
}

func parseColors(list string) []rgb {
	var out []rgb
	for _, p := range strings.Split(list, ",") {
		if c, ok := parseHex(p); ok {
			out = append(out, c)
		}
	}
	return out
}

var cavaKV = regexp.MustCompile(`^\s*([a-z_0-9]+)\s*=\s*(.+?)\s*$`)

// cavaColors reads the [color] section of an existing cava config (and the
// theme file it names), so switching from cava keeps your look.
func cavaColors() []rgb {
	dir := cavaDir()
	kv := readIni(filepath.Join(dir, "config"), "color")
	if theme := strings.Trim(kv["theme"], `'"`); theme != "" && theme != "none" {
		for k, v := range readIni(filepath.Join(dir, "themes", theme), "color") {
			if _, set := kv[k]; !set {
				kv[k] = v
			}
		}
	}
	var out []rgb
	if kv["gradient"] == "1" {
		for i := 1; i <= 8; i++ {
			if c, ok := parseHex(kv["gradient_color_"+strconv.Itoa(i)]); ok {
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		if c, ok := parseHex(kv["foreground"]); ok {
			out = []rgb{c}
		}
	}
	return out
}

func cavaDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "cava")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "cava")
}

func readIni(path, section string) map[string]string {
	kv := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return kv
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = strings.EqualFold(strings.Trim(line, "[]"), section)
			continue
		}
		if !in || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if m := cavaKV.FindStringSubmatch(line); m != nil {
			kv[m[1]] = strings.Trim(m[2], `'"`)
		}
	}
	return kv
}
