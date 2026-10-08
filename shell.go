// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"path"
	"regexp"
	"strings"
)

// Target kinds sent to the remote checker.
const (
	TExec  = 'x' // must be executable
	TBare  = 'b' // bare command name, looked up in cron's PATH
	TRead  = 'r' // must be readable (script given to an interpreter, input redirect)
	TWrite = 'w' // must be writable/creatable (output redirect, lock file)
	TDir   = 'd' // must be a traversable directory (cd)
)

// Target is something a cron command needs in order to run.
type Target struct {
	Kind    byte
	Role    string // "command", "script", "interpreter", "output", ...
	Word    string // as written in the command
	Path    string // resolved absolute path, or bare name for TBare
	PathVar string // PATH used for lookups
}

// Extraction is the result of analysing one cron command.
type Extraction struct {
	Targets []Target
	Notes   []string // things that could not be checked
}

type word struct {
	val        string
	raw        string // exactly as written in the command
	op         string // non-empty for operators/redirections
	unresolved string // "$FOO", "$(...)" etc. if it can't be expanded statically
	glob       bool
}

// lex tokenises a POSIX shell command line, expanding ~ and $VAR from env.
func lex(s string, env map[string]string) []word {
	var out []word
	ops := []string{"&>>", "<<<", "&&", "||", ";;", ">>", "&>", ">&", "<&", ">|", "<<", "<>", ";", "|", "&", "(", ")", ">", "<"}
	i := 0
	for i < len(s) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' {
			i++
			continue
		}
		if c == '#' {
			break
		}
		// fd-prefixed redirection: 2>, 2>>, 2>&
		j := i
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i && j < len(s) && (s[j] == '>' || s[j] == '<') {
			for _, op := range ops {
				if strings.HasPrefix(s[j:], op) && strings.ContainsAny(op, "<>") {
					out = append(out, word{op: s[i:j] + op})
					i = j + len(op)
					break
				}
			}
			continue
		}
		matched := false
		for _, op := range ops {
			if strings.HasPrefix(s[i:], op) {
				out = append(out, word{op: op})
				i += len(op)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		var w word
		var b strings.Builder
		start := i
		for i < len(s) {
			c := s[i]
			if strings.IndexByte(" \t\n;&|()<>", c) >= 0 {
				break
			}
			switch c {
			case '\'':
				end := strings.IndexByte(s[i+1:], '\'')
				if end < 0 {
					b.WriteString(s[i+1:])
					i = len(s)
				} else {
					b.WriteString(s[i+1 : i+1+end])
					i += end + 2
				}
			case '"':
				i++
				for i < len(s) && s[i] != '"' {
					switch {
					case s[i] == '\\' && i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[i+1]) >= 0:
						b.WriteByte(s[i+1])
						i += 2
					case s[i] == '$':
						i = expandVar(s, i, env, &b, &w)
					case s[i] == '`':
						i = skipBackticks(s, i, &w)
					default:
						b.WriteByte(s[i])
						i++
					}
				}
				i++
			case '\\':
				if i+1 < len(s) {
					b.WriteByte(s[i+1])
				}
				i += 2
			case '$':
				i = expandVar(s, i, env, &b, &w)
			case '`':
				i = skipBackticks(s, i, &w)
			case '~':
				if i == start && (i+1 == len(s) || s[i+1] == '/' || strings.IndexByte(" \t;&|()<>", s[i+1]) >= 0) {
					if h := env["HOME"]; h != "" {
						b.WriteString(h)
					} else if w.unresolved == "" {
						w.unresolved = "~"
					}
				} else {
					b.WriteByte(c)
				}
				i++
			case '*', '?', '[':
				w.glob = true
				b.WriteByte(c)
				i++
			default:
				b.WriteByte(c)
				i++
			}
		}
		w.val = b.String()
		w.raw = s[start:min(i, len(s))]
		out = append(out, w)
	}
	return out
}

func expandVar(s string, i int, env map[string]string, b *strings.Builder, w *word) int {
	i++ // skip $
	if i >= len(s) {
		b.WriteByte('$')
		return i
	}
	switch {
	case s[i] == '(':
		depth := 0
		j := i
		for ; j < len(s); j++ {
			if s[j] == '(' {
				depth++
			} else if s[j] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if w.unresolved == "" {
			w.unresolved = "$" + s[i:min(j+1, len(s))]
		}
		return j + 1
	case s[i] == '{':
		end := strings.IndexByte(s[i:], '}')
		if end < 0 {
			return len(s)
		}
		name := s[i+1 : i+end]
		if v, ok := env[name]; ok && isName(name) {
			b.WriteString(v)
		} else if w.unresolved == "" {
			w.unresolved = "${" + name + "}"
		}
		return i + end + 1
	case isNameStart(s[i]):
		j := i
		for j < len(s) && isNameChar(s[j]) {
			j++
		}
		name := s[i:j]
		if v, ok := env[name]; ok {
			b.WriteString(v)
		} else if w.unresolved == "" {
			w.unresolved = "$" + name
		}
		return j
	case strings.IndexByte("0123456789@*#?$!-", s[i]) >= 0:
		if w.unresolved == "" {
			w.unresolved = "$" + string(s[i])
		}
		return i + 1
	}
	b.WriteByte('$')
	return i
}

func skipBackticks(s string, i int, w *word) int {
	end := strings.IndexByte(s[i+1:], '`')
	if w.unresolved == "" {
		w.unresolved = "`...`"
	}
	if end < 0 {
		return len(s)
	}
	return i + end + 2
}

func isNameStart(c byte) bool { return c == '_' || (c|0x20) >= 'a' && (c|0x20) <= 'z' }
func isNameChar(c byte) bool  { return isNameStart(c) || c >= '0' && c <= '9' }
func isName(s string) bool {
	if s == "" || !isNameStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isNameChar(s[i]) {
			return false
		}
	}
	return true
}

var assignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

var builtins = map[string]bool{
	"echo": true, "printf": true, "test": true, "[": true, "[[": true, ":": true, "true": true,
	"false": true, "exit": true, "export": true, "unset": true, "set": true, "read": true,
	"eval": true, "trap": true, "wait": true, "kill": true, "umask": true, "ulimit": true,
	"shift": true, "return": true, "break": true, "continue": true, "local": true, "type": true,
	"hash": true, "alias": true, "getopts": true, "times": true, "let": true, "declare": true,
	"typeset": true, "readonly": true,
}

var reserved = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true, "do": true, "done": true,
	"while": true, "until": true, "!": true, "{": true, "}": true, "time": true,
}

type wrapSpec struct {
	argOpts    []string // options that consume the next word
	positional int      // leading positional args before the wrapped command
	lockFile   bool     // first positional is a lock file (flock)
	cmdOpts    []string // options whose argument is a shell command string (flock -c)
	assigns    bool     // NAME=value words allowed (env)
}

var wrappers = map[string]wrapSpec{
	"nohup":       {},
	"setsid":      {},
	"chronic":     {},
	"cronic":      {},
	"run-one":     {},
	"exec":        {argOpts: []string{"-a"}},
	"command":     {},
	"builtin":     {},
	"nice":        {argOpts: []string{"-n", "--adjustment"}},
	"ionice":      {argOpts: []string{"-c", "-n", "--class", "--classdata"}},
	"timeout":     {argOpts: []string{"-s", "-k", "--signal", "--kill-after"}, positional: 1},
	"flock":       {argOpts: []string{"-w", "-E", "--timeout", "--wait", "--conflict-exit-code"}, positional: 1, lockFile: true, cmdOpts: []string{"-c", "--command"}},
	"env":         {argOpts: []string{"-u", "-C", "--unset", "--chdir"}, assigns: true},
	"stdbuf":      {argOpts: []string{"-i", "-o", "-e"}},
	"taskset":     {positional: 1},
	"chrt":        {positional: 1},
	"systemd-cat": {argOpts: []string{"-t", "-p", "--identifier", "--priority"}},
}

var interpRe = regexp.MustCompile(`^(sh|bash|dash|ksh|zsh|mksh|ash|python[0-9.]*|pypy[0-9.]*|perl[0-9.]*|ruby[0-9.]*|php[0-9.]*|node|nodejs|Rscript|lua[0-9.]*|tclsh[0-9.]*|java)$`)
var shellRe = regexp.MustCompile(`^(sh|bash|dash|ksh|zsh|mksh|ash)$`)

type extractor struct {
	env   map[string]string
	cwd   string
	depth int
	ex    *Extraction
}

// ExtractTargets finds files a cron command depends on. env is the cron
// environment (PATH, HOME, crontab variables); cron starts jobs in $HOME.
func ExtractTargets(cmd string, env map[string]string) *Extraction {
	x := &extractor{env: env, cwd: env["HOME"], ex: &Extraction{}}
	x.run(CronShellPart(cmd))
	// de-duplicate
	seen := map[string]bool{}
	var ts []Target
	for _, t := range x.ex.Targets {
		k := string(t.Kind) + "\x00" + t.Path + "\x00" + t.PathVar
		if !seen[k] {
			seen[k] = true
			ts = append(ts, t)
		}
	}
	x.ex.Targets = ts
	return x.ex
}

func (x *extractor) note(s string) {
	for _, n := range x.ex.Notes {
		if n == s {
			return
		}
	}
	x.ex.Notes = append(x.ex.Notes, s)
}

func (x *extractor) run(cmd string) {
	if x.depth > 3 {
		return
	}
	words := lex(cmd, x.env)
	var seg []word
	flush := func() {
		if len(seg) > 0 {
			x.segment(seg)
		}
		seg = nil
	}
	for _, w := range words {
		switch w.op {
		case "&&", "||", ";", "|", "&", "(", ")", ";;":
			flush()
		default:
			seg = append(seg, w)
		}
	}
	flush()
}

func (x *extractor) resolve(w word) (string, bool) {
	if w.unresolved != "" {
		x.note("not checked: " + w.val + " uses " + w.unresolved + " (only known at run time)")
		return "", false
	}
	if w.glob {
		x.note("not checked: " + w.val + " contains a wildcard")
		return "", false
	}
	v := w.val
	if v == "" {
		return "", false
	}
	if !strings.HasPrefix(v, "/") {
		if x.cwd == "" {
			x.note("not checked: relative path " + v + " (home directory unknown)")
			return "", false
		}
		v = path.Join(x.cwd, v)
	}
	return path.Clean(v), true
}

func (x *extractor) add(kind byte, role string, w word, pathVar string) {
	if kind == TExec && !strings.Contains(w.val, "/") && w.unresolved == "" && !w.glob {
		x.ex.Targets = append(x.ex.Targets, Target{Kind: TBare, Role: role, Word: w.raw, Path: w.val, PathVar: pathVar})
		return
	}
	p, ok := x.resolve(w)
	if !ok {
		return
	}
	switch p {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty":
		return
	}
	if strings.HasPrefix(p, "/dev/fd/") || strings.HasPrefix(p, "/proc/self/fd/") {
		return
	}
	x.ex.Targets = append(x.ex.Targets, Target{Kind: kind, Role: role, Word: w.raw, Path: p, PathVar: pathVar})
}

func (x *extractor) segment(seg []word) {
	// Pull out redirections first.
	var ws []word
	for i := 0; i < len(seg); i++ {
		w := seg[i]
		if w.op == "" {
			ws = append(ws, w)
			continue
		}
		if i+1 >= len(seg) || seg[i+1].op != "" {
			continue
		}
		target := seg[i+1]
		i++
		op := strings.TrimLeft(w.op, "0123456789")
		switch {
		case op == "<<" || op == "<<<":
		case (op == ">&" || op == "<&") && (target.val == "-" || isDigits(target.val)):
		case strings.Contains(op, ">") || op == "<>":
			x.add(TWrite, "output", target, "")
		case op == "<":
			x.add(TRead, "input", target, "")
		}
	}

	pathVar := x.env["PATH"]
	i := 0
	// Leading VAR=value assignments.
	for i < len(ws) && assignRe.MatchString(ws[i].val) && ws[i].unresolved == "" {
		if strings.HasPrefix(ws[i].val, "PATH=") {
			pathVar = strings.TrimPrefix(ws[i].val, "PATH=")
		}
		i++
	}
	for i < len(ws) {
		w := ws[i]
		if reserved[w.val] {
			i++
			continue
		}
		switch w.val {
		case "for", "case", "select", "function":
			x.note("not checked: shell '" + w.val + "' construct")
			return
		}
		if w.unresolved != "" {
			x.note("not checked: command " + w.val + " uses " + w.unresolved)
			return
		}
		hasSlash := strings.Contains(w.val, "/")
		base := path.Base(w.val)
		if !hasSlash {
			switch {
			case w.val == "cd":
				if i+1 < len(ws) && ws[i+1].val != "-" {
					if p, ok := x.resolve(ws[i+1]); ok {
						x.ex.Targets = append(x.ex.Targets, Target{Kind: TDir, Role: "cd directory", Word: ws[i+1].raw, Path: p})
						x.cwd = p
					}
				} else if i+1 >= len(ws) {
					x.cwd = x.env["HOME"]
				}
				return
			case w.val == "." || w.val == "source":
				if i+1 < len(ws) {
					x.add(TRead, "sourced file", ws[i+1], "")
				}
				return
			case builtins[w.val]:
				return
			}
		}
		spec, isWrapper := wrappers[base]
		if base == "sudo" || base == "doas" || base == "su" || base == "runuser" {
			x.add(TExec, "command", w, pathVar)
			x.note("not checked: command after " + base + " runs as another user")
			return
		}
		if base == "run-parts" {
			x.add(TExec, "command", w, pathVar)
			for _, a := range ws[i+1:] {
				if !strings.HasPrefix(a.val, "-") {
					if p, ok := x.resolve(a); ok {
						x.ex.Targets = append(x.ex.Targets, Target{Kind: TDir, Role: "run-parts directory", Word: a.raw, Path: p})
					}
					break
				}
			}
			return
		}
		if isWrapper {
			if !(base == "exec" || base == "command" || base == "builtin") || hasSlash {
				x.add(TExec, "wrapper", w, pathVar)
			}
			i++
			// options
			for i < len(ws) {
				a := ws[i].val
				if spec.assigns && assignRe.MatchString(a) {
					if strings.HasPrefix(a, "PATH=") {
						pathVar = strings.TrimPrefix(a, "PATH=")
					}
					i++
					continue
				}
				if a == "--" {
					i++
					break
				}
				if !strings.HasPrefix(a, "-") || a == "-" {
					break
				}
				if (base == "command") && (a == "-v" || a == "-V") {
					return
				}
				if contains(spec.cmdOpts, a) {
					if i+1 < len(ws) {
						x.depth++
						x.run(ws[i+1].val)
						x.depth--
					}
					return
				}
				if contains(spec.argOpts, a) {
					i += 2
				} else {
					i++
				}
			}
			for p := 0; p < spec.positional && i < len(ws); p++ {
				if spec.lockFile && p == 0 && !isDigits(ws[i].val) {
					x.add(TWrite, "lock file", ws[i], "")
				}
				i++
			}
			// flock FILE -c CMD (options after the lock file)
			if spec.lockFile && i < len(ws) && contains(spec.cmdOpts, ws[i].val) {
				if i+1 < len(ws) {
					x.depth++
					x.run(ws[i+1].val)
					x.depth--
				}
				return
			}
			continue
		}
		if interpRe.MatchString(base) {
			x.add(TExec, "interpreter", w, pathVar)
			x.interpreterArgs(base, ws[i+1:])
			return
		}
		x.add(TExec, "command", w, pathVar)
		return
	}
}

func (x *extractor) interpreterArgs(base string, args []word) {
	var stopOpts, argOpts []string
	scriptOpt := ""
	switch {
	case shellRe.MatchString(base):
		argOpts = []string{"-o", "+o"}
	case strings.HasPrefix(base, "python") || strings.HasPrefix(base, "pypy"):
		stopOpts = []string{"-c", "-m"}
		argOpts = []string{"-W", "-X"}
	case strings.HasPrefix(base, "perl"), strings.HasPrefix(base, "ruby"):
		stopOpts = []string{"-e", "-E"}
	case strings.HasPrefix(base, "php"):
		stopOpts = []string{"-r"}
		argOpts = []string{"-c", "-d", "-z"}
		scriptOpt = "-f"
	case base == "node" || base == "nodejs":
		stopOpts = []string{"-e", "--eval", "-p", "--print"}
		argOpts = []string{"-r", "--require", "--loader", "--import"}
	case base == "java":
		argOpts = []string{"-cp", "-classpath", "--class-path", "-p", "--module-path"}
		scriptOpt = "-jar"
	default:
		stopOpts = []string{"-e"}
	}
	for i := 0; i < len(args); i++ {
		a := args[i].val
		if shellRe.MatchString(base) && a == "-c" {
			if i+1 < len(args) {
				if args[i+1].unresolved != "" {
					x.note("not checked: " + base + " -c command uses " + args[i+1].unresolved)
					return
				}
				x.depth++
				x.run(args[i+1].val)
				x.depth--
			}
			return
		}
		if a == "--" {
			if i+1 < len(args) {
				x.add(TRead, "script", args[i+1], "")
			}
			return
		}
		if scriptOpt != "" && a == scriptOpt {
			if i+1 < len(args) {
				x.add(TRead, "script", args[i+1], "")
			}
			return
		}
		if contains(stopOpts, a) {
			return
		}
		if contains(argOpts, a) {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") || (shellRe.MatchString(base) && strings.HasPrefix(a, "+")) {
			continue
		}
		if base == "java" {
			return // main class name, nothing to check
		}
		x.add(TRead, "script", args[i], "")
		return
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// MainTarget returns the target that best represents "the file this job runs":
// the script given to an interpreter, otherwise the first executed command.
func MainTarget(ts []Target) *Target {
	var best *Target
	for i := range ts {
		t := &ts[i]
		if t.Role == "script" {
			return t
		}
		if t.Role == "command" && best == nil {
			best = t
		}
	}
	if best == nil {
		for i := range ts {
			if ts[i].Role == "interpreter" || ts[i].Role == "wrapper" {
				best = &ts[i]
			}
		}
	}
	return best
}
