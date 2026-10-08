// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"
)

// ---- colours -------------------------------------------------------------

var useColor = true

func initColor() {
	useColor = os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stdout.Fd())) && enableVT()
}

func sgr(code, s string) string {
	if !useColor || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func red(s string) string    { return sgr("31", s) }
func green(s string) string  { return sgr("32", s) }
func yellow(s string) string { return sgr("33", s) }
func cyan(s string) string   { return sgr("36", s) }
func dim(s string) string    { return sgr("2", s) }
func bold(s string) string   { return sgr("1", s) }
func inv(s string) string    { return sgr("7", s) }

func sevLabel(s Severity) string {
	switch s {
	case SevOK:
		return green("OK")
	case SevWarn:
		return yellow("WARN")
	case SevFail:
		return red("FAIL")
	}
	return dim("?")
}

func sevMark(s Severity) string {
	switch s {
	case SevOK:
		return green("✓")
	case SevWarn:
		return yellow("!")
	case SevFail:
		return red("✗")
	}
	return dim("·")
}

// visibleLen counts runes outside ANSI escape sequences.
func visibleLen(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			n++
		}
	}
	return n
}

// fit truncates plain text to w columns, adding an ellipsis if cut.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return "…"
	}
	return string(r[:w-1]) + "…"
}

func padRight(s string, w int) string {
	if n := visibleLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// ---- keyboard ------------------------------------------------------------

type Key struct {
	Name string // "up","down","left","right","home","end","pgup","pgdn","del","enter","esc","bs","tab","ctrl-x", or "" for Rune
	Rune rune
}

// keyReader reads keys synchronously (no background goroutine, so child
// processes such as "ssh -t ... sudo" get the terminal's input untouched).
// Escape sequences arrive in a single read from the terminal.
type keyReader struct {
	pending []byte
}

func newKeyReader() *keyReader { return &keyReader{} }

func (k *keyReader) fill() bool {
	buf := make([]byte, 64)
	n, err := os.Stdin.Read(buf)
	if n > 0 {
		k.pending = append(k.pending, buf[:n]...)
	}
	return n > 0 || err == nil
}

func (k *keyReader) next() (byte, bool) {
	if len(k.pending) == 0 {
		return 0, false
	}
	b := k.pending[0]
	k.pending = k.pending[1:]
	return b, true
}

// Read blocks for the next key press.
func (k *keyReader) Read() (Key, bool) {
	for len(k.pending) == 0 {
		if !k.fill() {
			return Key{}, false
		}
	}
	b, _ := k.next()
	switch b {
	case '\r', '\n':
		return Key{Name: "enter"}, true
	case 127, 8:
		return Key{Name: "bs"}, true
	case '\t':
		return Key{Name: "tab"}, true
	case 0x1b:
		if len(k.pending) == 0 || (k.pending[0] != '[' && k.pending[0] != 'O') {
			return Key{Name: "esc"}, true
		}
		k.next()
		var seq []byte
		for {
			c, ok := k.next()
			if !ok {
				break
			}
			seq = append(seq, c)
			if c >= 0x40 && c <= 0x7e {
				break
			}
		}
		switch string(seq) {
		case "A":
			return Key{Name: "up"}, true
		case "B":
			return Key{Name: "down"}, true
		case "C":
			return Key{Name: "right"}, true
		case "D":
			return Key{Name: "left"}, true
		case "H", "1~", "7~":
			return Key{Name: "home"}, true
		case "F", "4~", "8~":
			return Key{Name: "end"}, true
		case "3~":
			return Key{Name: "del"}, true
		case "5~":
			return Key{Name: "pgup"}, true
		case "6~":
			return Key{Name: "pgdn"}, true
		}
		return Key{Name: "unknown"}, true
	}
	if b < 32 {
		return Key{Name: "ctrl-" + string(rune('a'+b-1))}, true
	}
	if b < 0x80 {
		return Key{Rune: rune(b)}, true
	}
	buf := []byte{b}
	for !utf8.FullRune(buf) && len(buf) < 4 {
		if len(k.pending) == 0 && !k.fill() {
			break
		}
		c, _ := k.next()
		buf = append(buf, c)
	}
	r, _ := utf8.DecodeRune(buf)
	return Key{Rune: r}, true
}

// ---- screen --------------------------------------------------------------

type screen struct {
	state *term.State
	keys  *keyReader
}

func openScreen() (*screen, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, fmt.Errorf("cronman needs an interactive terminal")
	}
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	s := &screen{state: st, keys: newKeyReader()}
	fmt.Print("\x1b[?1049h\x1b[?25l") // alternate screen, hide cursor
	return s, nil
}

// suspend leaves raw mode/alt screen so a child process can use the terminal.
func (s *screen) suspend() {
	fmt.Print("\x1b[?25h\x1b[?1049l")
	_ = term.Restore(int(os.Stdin.Fd()), s.state)
}

func (s *screen) resume() {
	st, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err == nil {
		s.state = st
	}
	fmt.Print("\x1b[?1049h\x1b[?25l")
}

func (s *screen) close() {
	s.suspend()
}

func (s *screen) size() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 20 || h < 8 {
		return 80, 24
	}
	return w, h
}

// draw paints lines (already width-fitted) and optionally places the cursor.
func (s *screen) draw(lines []string, curRow, curCol int) {
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(l)
		b.WriteString("\x1b[0m\x1b[K")
	}
	b.WriteString("\x1b[J")
	if curRow >= 0 {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", curRow+1, curCol+1)
	} else {
		b.WriteString("\x1b[?25l")
	}
	os.Stdout.WriteString(b.String())
}

// lineEditor is a single-line editor with the old value pre-filled.
type lineEditor struct {
	buf []rune
	pos int
}

// handle applies a key; returns done (enter) or cancelled (esc).
func (e *lineEditor) handle(k Key) (done, cancel bool) {
	switch k.Name {
	case "enter":
		return true, false
	case "esc", "ctrl-c", "ctrl-g":
		return false, true
	case "left", "ctrl-b":
		if e.pos > 0 {
			e.pos--
		}
	case "right", "ctrl-f":
		if e.pos < len(e.buf) {
			e.pos++
		}
	case "home", "ctrl-a":
		e.pos = 0
	case "end", "ctrl-e":
		e.pos = len(e.buf)
	case "bs":
		if e.pos > 0 {
			e.buf = append(e.buf[:e.pos-1], e.buf[e.pos:]...)
			e.pos--
		}
	case "del", "ctrl-d":
		if e.pos < len(e.buf) {
			e.buf = append(e.buf[:e.pos], e.buf[e.pos+1:]...)
		}
	case "ctrl-u":
		e.buf = append([]rune{}, e.buf[e.pos:]...)
		e.pos = 0
	case "ctrl-k":
		e.buf = e.buf[:e.pos]
	case "ctrl-w":
		i := e.pos
		for i > 0 && e.buf[i-1] == ' ' {
			i--
		}
		for i > 0 && e.buf[i-1] != ' ' {
			i--
		}
		e.buf = append(e.buf[:i], e.buf[e.pos:]...)
		e.pos = i
	case "":
		if k.Rune >= 32 {
			e.buf = append(e.buf[:e.pos], append([]rune{k.Rune}, e.buf[e.pos:]...)...)
			e.pos++
		}
	}
	return false, false
}

// view renders the editor in w columns, returning text and cursor column.
func (e *lineEditor) view(w int) (string, int) {
	if w < 2 {
		w = 2
	}
	start := 0
	if e.pos >= w {
		start = e.pos - w + 1
	}
	end := start + w
	if end > len(e.buf) {
		end = len(e.buf)
	}
	return string(e.buf[start:end]), e.pos - start
}

// ---- plain prompts (outside the TUI) ---------------------------------------

var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	s, err := stdinReader.ReadString('\n')
	return strings.TrimRight(s, "\r\n"), err
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return readLine("")
	}
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
