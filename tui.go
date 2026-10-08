package main

import (
	"fmt"
	"strings"
	"time"
)

type remoteInfo struct {
	User   string
	Groups []string
	Home   string
	OS     string
	Now    time.Time
}

func (ri remoteInfo) ctx() UserCtx {
	bsd := ri.OS == "Darwin" || strings.HasSuffix(ri.OS, "BSD") || ri.OS == "DragonFly"
	return UserCtx{User: ri.User, Groups: ri.Groups, BSD: bsd}
}

const infoScript = `
printf '@@USER %s\n' "$(id -un)"
printf '@@GROUPS %s\n' "$(id -Gn 2>/dev/null)"
printf '@@HOME %s\n' "$HOME"
printf '@@OS %s\n' "$(uname -s)"
printf '@@NOW %s\n' "$(date '+%Y-%m-%d %H:%M:%S %z')"
e="${TMPDIR:-/tmp}/.cronman.$$"
printf '@@CRON\n'
crontab -l 2>"$e"; rc=$?
printf '\n@@END %s\n' "$rc"
cat "$e" 2>/dev/null; rm -f "$e"
`

// loadRemote fetches user info and the crontab.
func loadRemote(r Runner) (remoteInfo, string, error) {
	var ri remoteInfo
	out, stderr, err := RunScript(r, "", infoScript)
	if err != nil && !strings.Contains(out, "@@END") {
		return ri, "", fmt.Errorf("%v %s", err, strings.TrimSpace(stderr))
	}
	head, rest, ok := strings.Cut(out, "@@CRON\n")
	if !ok {
		return ri, "", fmt.Errorf("unexpected response from host: %s", strings.TrimSpace(out+stderr))
	}
	for _, l := range strings.Split(head, "\n") {
		k, v, _ := strings.Cut(l, " ")
		switch k {
		case "@@USER":
			ri.User = v
		case "@@GROUPS":
			ri.Groups = strings.Fields(v)
		case "@@HOME":
			ri.Home = v
		case "@@OS":
			ri.OS = v
		case "@@NOW":
			if t, err := time.Parse("2006-01-02 15:04:05 -0700", v); err == nil {
				ri.Now = t
			}
		}
	}
	if ri.Now.IsZero() {
		ri.Now = time.Now()
	}
	i := strings.LastIndex(rest, "\n@@END ")
	if i < 0 {
		return ri, "", fmt.Errorf("unexpected response from host")
	}
	content := rest[:i]
	tail := rest[i+len("\n@@END "):]
	rc, errText, _ := strings.Cut(tail, "\n")
	if strings.TrimSpace(rc) != "0" {
		if strings.Contains(strings.ToLower(errText), "no crontab") {
			return ri, "", nil
		}
		return ri, "", fmt.Errorf("crontab -l failed: %s", strings.TrimSpace(errText))
	}
	return ri, content, nil
}

func normalizeCrontab(s string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// ---- app -----------------------------------------------------------------

type app struct {
	r      Runner
	scr    *screen
	info   remoteInfo
	orig   string
	ct     *Crontab
	jobs   []*Line
	rep    map[*Line]*JobReport
	ext    map[*Line]*Extraction
	sel    int
	off    int
	status string
}

func runInteractive(r Runner) error {
	fmt.Fprintf(stderr(), "Connecting to %s…\n", r.Label())
	ri, content, err := loadRemote(r)
	if err != nil {
		return err
	}
	a := &app{r: r, info: ri}
	a.setContent(content)
	fmt.Fprintf(stderr(), "Checking %d job(s)…\n", len(a.jobs))
	a.checkAll()
	scr, err := openScreen()
	if err != nil {
		return err
	}
	a.scr = scr
	defer scr.close()
	a.status = "Loaded " + plural(len(a.jobs), "job") + ". Press ? for help."
	a.loop()
	return nil
}

func plural(n int, s string) string {
	if n == 1 {
		return "1 " + s
	}
	return fmt.Sprintf("%d %ss", n, s)
}

func (a *app) setContent(content string) {
	a.orig = content
	a.ct = ParseCrontab(content, false)
	a.jobs = a.ct.Jobs()
	a.rep = map[*Line]*JobReport{}
	a.ext = map[*Line]*Extraction{}
	if a.sel >= len(a.jobs) {
		a.sel = max(0, len(a.jobs)-1)
	}
}

func (a *app) dirty() bool { return a.ct.Render() != normalizeCrontab(a.orig) }

func (a *app) checkAll() { a.check(a.jobs) }

func (a *app) check(jobs []*Line) {
	var reqs []CheckReq
	idx := map[string]bool{}
	for _, l := range jobs {
		ex := ExtractTargets(l.Command, a.ct.EnvAt(l, a.info.Home))
		a.ext[l] = ex
		for _, t := range ex.Targets {
			rq := reqFor(t)
			if !idx[rq.key()] {
				idx[rq.key()] = true
				reqs = append(reqs, rq)
			}
		}
	}
	res, err := RunChecks(a.r, reqs)
	if err != nil {
		a.status = red("check failed: " + err.Error())
		return
	}
	ctx := a.info.ctx()
	for _, l := range jobs {
		ex := a.ext[l]
		jr := &JobReport{Notes: ex.Notes}
		for _, t := range ex.Targets {
			jr.Targets = append(jr.Targets, Analyze(t, res[reqFor(t).key()], ctx))
		}
		a.rep[l] = jr
	}
}

func (a *app) current() *Line {
	if a.sel < 0 || a.sel >= len(a.jobs) {
		return nil
	}
	return a.jobs[a.sel]
}

// ---- rendering -------------------------------------------------------------

func key(k string) string { return bold(cyan(k)) }

func keybar(w int, items [][2]string) []string {
	var lines []string
	cur, curLen := " ", 1
	for _, it := range items {
		seg := key(it[0]) + " " + it[1]
		sl := visibleLen(seg)
		if curLen+sl+2 > w && curLen > 1 {
			lines = append(lines, cur)
			cur, curLen = " ", 1
		}
		if curLen > 1 {
			cur += "  "
			curLen += 2
		}
		cur += seg
		curLen += sl
	}
	return append(lines, cur)
}

var mainKeys = [][2]string{
	{"↑↓", "move"}, {"space", "enable/disable"}, {"e", "edit schedule"}, {"c", "edit command"},
	{"p", "change script path"}, {"enter", "details & fixes"}, {"w", "save"}, {"d", "diff"},
	{"r", "reload"}, {"R", "re-check"}, {"?", "help"}, {"q", "quit"},
}

func (a *app) titleLine(w int) string {
	n, off := len(a.jobs), 0
	fail := 0
	for _, l := range a.jobs {
		if l.Disabled {
			off++
		}
		if a.rep[l].Sev() == SevFail && !l.Disabled {
			fail++
		}
	}
	t := inv(bold(" cronman ")) + " " + bold(a.info.User+"@"+hostOf(a.r.Label())) +
		dim(fmt.Sprintf("  %d jobs, %d disabled", n, off))
	if fail > 0 {
		t += "  " + red(fmt.Sprintf("%d failing", fail))
	}
	if a.dirty() {
		t += "  " + yellow("● unsaved changes (w to save)")
	}
	return t
}

func hostOf(label string) string {
	if i := strings.LastIndex(label, "@"); i >= 0 {
		return label[i+1:]
	}
	return label
}

func (a *app) render(promptLines []string) []string {
	w, h := a.scr.size()
	keys := keybar(w, mainKeys)
	sep := dim(strings.Repeat("─", w))
	fixed := 1 + 1 + 1 + 4 + 1 + len(keys) + 1
	if promptLines != nil {
		fixed += len(promptLines) - 1
	}
	listH := h - fixed
	if listH < 3 {
		listH = 3
	}
	lines := []string{a.titleLine(w)}
	schedW := 13
	for _, l := range a.jobs {
		if n := len(l.Schedule); n > schedW {
			schedW = min(n, 24)
		}
	}
	lines = append(lines, dim(fmt.Sprintf("   %3s  %-5s %-5s %-*s %s", "#", "STATE", "CHECK", schedW, "SCHEDULE", "COMMAND")))
	if a.sel < a.off {
		a.off = a.sel
	}
	if a.sel >= a.off+listH {
		a.off = a.sel - listH + 1
	}
	for i := 0; i < listH; i++ {
		j := a.off + i
		if j >= len(a.jobs) {
			if len(a.jobs) == 0 && i == 0 {
				lines = append(lines, "   "+dim("(no cron jobs for "+a.info.User+")"))
				continue
			}
			lines = append(lines, "")
			continue
		}
		l := a.jobs[j]
		state := green("ON ")
		if l.Disabled {
			state = dim("OFF")
		}
		mark := "  "
		if j == a.sel {
			mark = cyan("▶ ")
		}
		pre := fmt.Sprintf("%s%3d  %s   %s ", mark, j+1, state, padRight(sevLabel(a.rep[l].Sev()), 5))
		sched := fmt.Sprintf("%-*s ", schedW, fit(l.Schedule, schedW))
		rest := w - visibleLen(pre) - len(sched) - 1
		cmd := fit(l.Command, rest)
		if l.Modified {
			sched = yellow(sched)
		}
		body := sched + cmd
		switch {
		case j == a.sel:
			body = bold(body)
		case l.Disabled:
			body = dim(body)
		}
		lines = append(lines, pre+body)
	}
	lines = append(lines, sep)
	lines = append(lines, a.summaryPanel(w)...)
	lines = append(lines, sep)
	lines = append(lines, keys...)
	if promptLines != nil {
		lines = append(lines, promptLines...)
	} else {
		lines = append(lines, " "+fit(a.status, w-1))
	}
	return lines
}

// summaryPanel shows 4 lines about the selected job.
func (a *app) summaryPanel(w int) []string {
	out := make([]string, 0, 4)
	l := a.current()
	if l == nil {
		return []string{"", "", "", ""}
	}
	desc := Describe(l.Schedule)
	info := fmt.Sprintf(" line %d", l.Num)
	if desc != "" {
		info += " · " + desc
	}
	if nx := a.nextRuns(l.Schedule, 3); nx != "" {
		info += " · next: " + nx
	}
	out = append(out, dim(fit(info, w)))
	jr := a.rep[l]
	if jr != nil {
		if mt := MainTarget(a.ext[l].Targets); mt != nil {
			for _, tr := range jr.Targets {
				if tr.T.Word == mt.Word && tr.T.Kind == mt.Kind {
					s := tr.Summary
					if s == "" && len(tr.Issues) > 0 {
						s = tr.Issues[0].Msg
					}
					out = append(out, " "+sevMark(tr.Sev())+" "+fit(tr.T.Path+" — "+s, w-4))
				}
			}
		}
		for _, tr := range jr.Targets {
			for _, is := range tr.Issues {
				if len(out) >= 4 || is.Sev < SevWarn {
					continue
				}
				line := " " + sevMark(is.Sev) + " " + fit(is.Msg, w-4)
				if !containsStr(out, line) {
					out = append(out, line)
				}
			}
		}
		if len(out) < 4 && jr.Sev() >= SevWarn {
			out = append(out, dim("   press enter for details and proposed fixes"))
		}
	}
	for len(out) < 4 {
		out = append(out, "")
	}
	return out[:4]
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (a *app) nextRuns(sched string, n int) string {
	if strings.EqualFold(sched, "@reboot") {
		return "at next boot"
	}
	ts, err := NextRuns(sched, a.info.Now, n)
	if err != nil || len(ts) == 0 {
		return ""
	}
	var parts []string
	for i, t := range ts {
		f := "Mon 01-02 15:04"
		if i > 0 && t.YearDay() == ts[i-1].YearDay() {
			f = "15:04"
		}
		parts = append(parts, t.Format(f))
	}
	return strings.Join(parts, ", ")
}

// ---- interaction -----------------------------------------------------------

func (a *app) loop() {
	for {
		a.scr.draw(a.render(nil), -1, -1)
		k, ok := a.scr.keys.Read()
		if !ok {
			return
		}
		a.status = ""
		switch {
		case k.Name == "up" || k.Rune == 'k':
			if a.sel > 0 {
				a.sel--
			}
		case k.Name == "down" || k.Rune == 'j':
			if a.sel < len(a.jobs)-1 {
				a.sel++
			}
		case k.Name == "pgup":
			a.sel = max(0, a.sel-10)
		case k.Name == "pgdn":
			a.sel = max(0, min(len(a.jobs)-1, a.sel+10))
		case k.Name == "home" || k.Rune == 'g':
			a.sel = 0
		case k.Name == "end" || k.Rune == 'G':
			a.sel = max(0, len(a.jobs)-1)
		case k.Rune == ' ' || k.Rune == 't':
			a.toggle()
		case k.Rune == 'e' || k.Rune == 's':
			a.editSchedule(nil)
		case k.Rune == 'c':
			a.editCommand(nil)
		case k.Rune == 'p' || k.Rune == 'f':
			a.editPath(nil)
		case k.Name == "enter" || k.Name == "right" || k.Rune == 'l':
			if a.current() != nil {
				a.details()
			}
		case k.Rune == 'w':
			a.save()
		case k.Rune == 'd':
			a.showDiff(false)
		case k.Rune == 'r':
			a.reload()
		case k.Rune == 'R':
			a.status = "Re-checking…"
			a.scr.draw(a.render(nil), -1, -1)
			a.checkAll()
			a.status = "Checks refreshed."
		case k.Rune == '?' || k.Rune == 'h':
			a.help()
		case k.Rune == 'q' || k.Name == "ctrl-c" || k.Name == "esc":
			if a.confirmQuit() {
				return
			}
		}
	}
}

func (a *app) toggle() {
	l := a.current()
	if l == nil {
		return
	}
	l.SetDisabled(!l.Disabled)
	if l.Disabled {
		a.status = fmt.Sprintf("Job %d commented out (press w to save).", a.sel+1)
	} else {
		a.status = fmt.Sprintf("Job %d enabled (press w to save).", a.sel+1)
		if a.rep[l].Sev() == SevFail {
			a.status += " " + red("Warning: its checks are failing.")
		}
	}
}

// prompt edits a value on the bottom lines. base renders the screen behind it.
func (a *app) prompt(base func([]string) []string, label, initial string, validate func(string) string) (string, bool) {
	ed := &lineEditor{buf: []rune(initial), pos: len([]rune(initial))}
	msg := ""
	for {
		w, _ := a.scr.size()
		hint := dim("  enter=accept  esc=cancel  ←→/home/end move  ctrl-u clear")
		head := " " + bold(label) + hint
		if msg != "" {
			head = " " + red(msg)
		}
		text, col := ed.view(w - 4)
		pl := []string{head, " " + cyan("›") + " " + text}
		lines := base(pl)
		a.scr.draw(lines, len(lines)-1, 3+col)
		k, ok := a.scr.keys.Read()
		if !ok {
			return "", false
		}
		done, cancel := ed.handle(k)
		if cancel {
			return "", false
		}
		if done {
			v := string(ed.buf)
			if validate != nil {
				if m := validate(v); m != "" {
					msg = m
					continue
				}
			}
			return v, true
		}
		msg = ""
	}
}

func (a *app) editSchedule(base func([]string) []string) {
	l := a.current()
	if l == nil {
		return
	}
	if base == nil {
		base = a.render
	}
	v, ok := a.prompt(base, "New schedule (min hour dom mon dow, or @daily/@hourly/@reboot…):", l.Schedule, func(s string) string {
		if err := ValidateSchedule(strings.Join(strings.Fields(s), " ")); err != nil {
			return "invalid: " + err.Error()
		}
		return ""
	})
	if !ok {
		a.status = "Cancelled."
		return
	}
	if strings.Join(strings.Fields(v), " ") == l.Schedule {
		a.status = "Schedule unchanged."
		return
	}
	_ = l.SetSchedule(v)
	a.status = "Schedule → " + l.Schedule
	if d := Describe(l.Schedule); d != "" {
		a.status += " (" + d + ")"
	}
	if nx := a.nextRuns(l.Schedule, 3); nx != "" {
		a.status += ", next: " + nx
	}
	a.status += ". Press w to save."
}

func (a *app) editCommand(base func([]string) []string) {
	l := a.current()
	if l == nil {
		return
	}
	if base == nil {
		base = a.render
	}
	v, ok := a.prompt(base, "Edit command:", l.Command, func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "command cannot be empty"
		}
		return ""
	})
	if !ok || strings.TrimSpace(v) == l.Command {
		a.status = "Command unchanged."
		return
	}
	_ = l.SetCommand(v)
	a.check([]*Line{l})
	a.status = "Command updated and re-checked: " + sevLabel(a.rep[l].Sev()) + ". Press w to save."
}

func (a *app) editPath(base func([]string) []string) {
	l := a.current()
	if l == nil {
		return
	}
	if base == nil {
		base = a.render
	}
	ex := a.ext[l]
	if ex == nil {
		ex = ExtractTargets(l.Command, a.ct.EnvAt(l, a.info.Home))
	}
	mt := MainTarget(ex.Targets)
	if mt == nil {
		a.status = "No script/executable path found in this command; use c to edit the whole command."
		return
	}
	v, ok := a.prompt(base, "New path for "+mt.Word+":", mt.Word, func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "path cannot be empty"
		}
		return ""
	})
	if !ok || v == mt.Word {
		a.status = "Path unchanged."
		return
	}
	nc, found := ApplyEdit(l.Command, mt.Word, strings.TrimSpace(v))
	if !found {
		a.status = red("Could not locate " + mt.Word + " in the command; use c to edit it.")
		return
	}
	_ = l.SetCommand(nc)
	a.check([]*Line{l})
	a.status = "Path updated and re-checked: " + sevLabel(a.rep[l].Sev()) + ". Press w to save."
}

func (a *app) ask(base func([]string) []string, question string, choices string) rune {
	for {
		lines := base([]string{" " + bold(question), " " + dim(choices)})
		a.scr.draw(lines, -1, -1)
		k, ok := a.scr.keys.Read()
		if !ok {
			return 0
		}
		if k.Name == "esc" || k.Name == "ctrl-c" {
			return 0
		}
		if k.Name == "enter" {
			return '\n'
		}
		if k.Rune != 0 {
			return k.Rune
		}
	}
}

func (a *app) confirmQuit() bool {
	if !a.dirty() {
		return true
	}
	switch a.ask(a.render, "You have unsaved changes.", "s = save and quit   d = discard and quit   esc = cancel") {
	case 's', 'S':
		return a.save()
	case 'd', 'D':
		return true
	}
	return false
}

func (a *app) reload() {
	if a.dirty() {
		if r := a.ask(a.render, "Reload from host and discard your unsaved changes?", "y = yes   any other key = no"); r != 'y' && r != 'Y' {
			return
		}
	}
	a.status = "Reloading…"
	a.scr.draw(a.render(nil), -1, -1)
	ri, content, err := loadRemote(a.r)
	if err != nil {
		a.status = red("reload failed: " + err.Error())
		return
	}
	a.info = ri
	a.setContent(content)
	a.checkAll()
	a.status = "Reloaded " + plural(len(a.jobs), "job") + "."
}

// diffLines shows changed lines (line count never changes in cronman).
func (a *app) diffLines() []string {
	var out []string
	for _, l := range a.ct.Lines {
		if r := l.Render(); r != l.Raw {
			out = append(out, dim(fmt.Sprintf(" line %d:", l.Num)))
			out = append(out, red(" - "+l.Raw))
			out = append(out, green(" + "+r))
		}
	}
	return out
}

// showDiff displays pending changes; with confirm it asks y/n and returns the answer.
func (a *app) showDiff(confirm bool) bool {
	d := a.diffLines()
	if len(d) == 0 {
		a.status = "No changes."
		return false
	}
	off := 0
	for {
		w, h := a.scr.size()
		lines := []string{inv(bold(" Pending changes ")) + " " + dim(a.r.Label()), ""}
		body := h - 5
		for i := off; i < len(d) && i < off+body; i++ {
			lines = append(lines, fit(d[i], w+20))
		}
		for len(lines) < h-2 {
			lines = append(lines, "")
		}
		if confirm {
			lines = append(lines, " "+bold("Write this crontab to "+a.r.Label()+"?"), " "+key("y")+" save   "+key("n/esc")+" cancel   "+key("↑↓")+" scroll")
		} else {
			lines = append(lines, "", " "+key("↑↓")+" scroll   "+key("w")+" save   "+key("esc/q")+" back")
		}
		a.scr.draw(lines, -1, -1)
		k, ok := a.scr.keys.Read()
		if !ok {
			return false
		}
		switch {
		case k.Name == "up":
			off = max(0, off-1)
		case k.Name == "down":
			if off+body < len(d) {
				off++
			}
		case confirm && (k.Rune == 'y' || k.Rune == 'Y'):
			return true
		case !confirm && k.Rune == 'w':
			a.save()
			return false
		case k.Rune == 'n' || k.Rune == 'q' || k.Name == "esc" || k.Name == "ctrl-c" || (!confirm && k.Name == "enter"):
			return false
		}
	}
}

func (a *app) save() bool {
	if !a.dirty() {
		a.status = "Nothing to save."
		return true
	}
	if !a.showDiff(true) {
		a.status = "Save cancelled."
		return false
	}
	a.status = "Saving…"
	a.scr.draw(a.render(nil), -1, -1)
	_, current, err := loadRemote(a.r)
	if err != nil {
		a.status = red("save failed: " + err.Error())
		return false
	}
	if normalizeCrontab(current) != normalizeCrontab(a.orig) {
		a.status = red("Not saved: the crontab was changed on the host since it was loaded. Press r to reload (your edits will be lost).")
		return false
	}
	newContent := a.ct.Render()
	hs, body := heredoc(newContent)
	script := `umask 077
d="$HOME/.cronman_backups"
b="$d/crontab.$(date +%Y%m%d-%H%M%S)"
mkdir -p "$d" && crontab -l > "$b" 2>/dev/null && echo "@@BACKUP $b"
ls -1t "$d"/crontab.* 2>/dev/null | tail -n +21 | while IFS= read -r f; do rm -f "$f"; done
crontab - ` + hs + "\n" + body + `echo "@@RC $?"
`
	out, stderr, err := RunScript(a.r, "", script)
	if !strings.Contains(out, "@@RC 0") {
		msg := strings.TrimSpace(stderr)
		if msg == "" && err != nil {
			msg = err.Error()
		}
		a.status = red("crontab rejected the change: " + oneLine(msg))
		return false
	}
	backup := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "@@BACKUP ") {
			backup = strings.TrimPrefix(l, "@@BACKUP ")
		}
	}
	_, verify, err := loadRemote(a.r)
	if err != nil || normalizeCrontab(verify) != newContent {
		a.status = yellow("Saved, but the read-back differs from what was written; press r to reload.")
		return true
	}
	sel := a.sel
	a.setContent(verify)
	a.sel = min(sel, max(0, len(a.jobs)-1))
	a.checkAll()
	a.status = green("Saved.")
	if backup != "" {
		a.status += " Previous crontab backed up to " + backup
	}
	return true
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (a *app) help() {
	lines := []string{
		inv(bold(" cronman — keys ")),
		"",
		bold(" Job list"),
		"   " + key("↑ ↓  j k") + "        move selection        " + key("PgUp PgDn  g G") + "  page / first / last",
		"   " + key("space  t") + "        comment out / un-comment the job",
		"   " + key("e") + "               edit the schedule (validated; shows the next run times)",
		"   " + key("c") + "               edit the whole command",
		"   " + key("p") + "               change only the script/executable path in the command",
		"   " + key("enter") + "           details: every file the job uses, problems and proposed fixes",
		"   " + key("w") + "               save to the host (shows a diff, backs up the old crontab first)",
		"   " + key("d") + "               show pending changes",
		"   " + key("r") + "               reload from the host     " + key("R") + "  re-run the permission checks",
		"   " + key("q  esc") + "          quit (asks if there are unsaved changes)",
		"",
		bold(" Details screen"),
		"   " + key("1-9") + "             apply proposed fix N (asks first; sudo fixes prompt for your password)",
		"   " + key("space e c p") + "     same as in the list     " + key("esc  q  ←") + "  back",
		"",
		bold(" Editing a value"),
		"   " + key("enter") + " accept   " + key("esc") + " cancel   " + key("← → home end") + " move   " + key("ctrl-u") + " clear   " + key("ctrl-w") + " delete word",
		"",
		bold(" Check column"),
		"   " + green("OK") + "   everything the job needs is in place for " + a.info.User,
		"   " + yellow("WARN") + " will probably run but something is off (e.g. CRLF line endings, no #! line)",
		"   " + red("FAIL") + " cron will fail to run it (missing file, no execute/read permission, bad interpreter…)",
		"   " + dim("?") + "    could not be checked (path built from variables or $(...))",
		"",
		dim(" Nothing is written to the host until you press w and confirm."),
		"",
		" press any key to return",
	}
	a.scr.draw(lines, -1, -1)
	a.scr.keys.Read()
}

// ---- details ---------------------------------------------------------------

type numberedFix struct {
	fix Fix
	tr  TargetReport
}

func (a *app) detailLines(w int) ([]string, []numberedFix) {
	l := a.current()
	var out []string
	var fixes []numberedFix
	state := green("ENABLED")
	if l.Disabled {
		state = dim("DISABLED (commented out)")
	}
	out = append(out, inv(bold(fmt.Sprintf(" Job %d ", a.sel+1)))+" "+state+dim(fmt.Sprintf("   crontab line %d", l.Num)))
	out = append(out, "")
	sched := l.Schedule
	if d := Describe(l.Schedule); d != "" {
		sched += dim("   (" + d + ")")
	}
	out = append(out, " "+bold("schedule ")+" "+sched)
	if nx := a.nextRuns(l.Schedule, 5); nx != "" {
		out = append(out, " "+bold("next runs")+" "+nx+dim("  (host time)"))
	}
	out = append(out, wrapPrefixed(" "+bold("command  ")+" ", "            ", l.Command, w)...)
	if strings.Contains(l.Command, "%") && CronShellPart(l.Command) != l.Command {
		out = append(out, dim("            note: text after an unescaped % is passed to the command as stdin"))
	}
	out = append(out, "")
	jr := a.rep[l]
	if jr == nil || len(jr.Targets) == 0 {
		out = append(out, " "+dim("No files to check were found in this command."))
	} else {
		out = append(out, " "+bold("Checks")+dim(" (as "+a.info.User+", with cron's environment)"))
		for _, tr := range jr.Targets {
			p := tr.T.Path
			if tr.T.Kind == TBare && tr.Res != nil && tr.Res["resolved"] != "" {
				p = tr.T.Word + " → " + tr.Res["resolved"]
			}
			head := fmt.Sprintf(" %s %s %s", sevMark(tr.Sev()), dim(fmt.Sprintf("%-12s", tr.T.Role)), p)
			if tr.Summary != "" {
				head += dim("  " + tr.Summary)
			}
			out = append(out, head)
			for _, is := range tr.Issues {
				out = append(out, wrapPrefixed("     "+sevMark(is.Sev)+" ", "       ", is.Msg, w)...)
				for _, f := range is.Fixes {
					if f.Cmd == "" && f.EditFrom == "" {
						out = append(out, wrapPrefixed("       "+dim("•")+" ", "         ", f.Desc, w)...)
						continue
					}
					fixes = append(fixes, numberedFix{f, tr})
					n := len(fixes)
					label := fmt.Sprintf("       %s ", key(fmt.Sprintf("[%d]", n)))
					out = append(out, wrapPrefixed(label, "           ", f.Desc, w)...)
					if f.Cmd != "" {
						out = append(out, "           "+cyan("$ "+a.fixCmdDisplay(f)))
					} else {
						out = append(out, "           "+cyan("command: "+f.EditFrom+" → "+f.EditTo))
					}
				}
			}
		}
	}
	if jr != nil {
		for _, n := range jr.Notes {
			out = append(out, " "+dim("· "+n))
		}
	}
	return out, fixes
}

func (a *app) fixCmdDisplay(f Fix) string {
	if f.Sudo && a.info.User != "root" {
		return "sudo sh -c " + shq(f.Cmd)
	}
	return f.Cmd
}

func wrapPrefixed(first, rest, text string, w int) []string {
	avail := w - visibleLen(first)
	if avail < 20 {
		avail = 20
	}
	var out []string
	r := []rune(text)
	prefix := first
	for len(r) > 0 {
		n := min(avail, len(r))
		if n < len(r) {
			if i := lastSpace(r[:n]); i > avail/2 {
				n = i + 1
			}
		}
		out = append(out, prefix+string(r[:n]))
		r = r[n:]
		prefix = rest
		avail = w - visibleLen(rest)
	}
	if len(out) == 0 {
		out = append(out, first)
	}
	return out
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ' ' {
			return i
		}
	}
	return -1
}

var detailKeys = [][2]string{
	{"1-9", "apply fix"}, {"space", "enable/disable"}, {"e", "schedule"}, {"c", "command"},
	{"p", "path"}, {"R", "re-check"}, {"↑↓", "scroll"}, {"esc/q", "back"},
}

func (a *app) details() {
	off := 0
	for {
		w, h := a.scr.size()
		body, fixes := a.detailLines(w)
		keys := keybar(w, detailKeys)
		var base func([]string) []string
		base = func(prompt []string) []string {
			bottom := len(keys) + 2
			if prompt != nil {
				bottom = len(keys) + 1 + len(prompt)
			}
			avail := h - bottom
			if off > max(0, len(body)-avail) {
				off = max(0, len(body)-avail)
			}
			var lines []string
			for i := off; i < len(body) && len(lines) < avail; i++ {
				lines = append(lines, body[i])
			}
			for len(lines) < avail {
				lines = append(lines, "")
			}
			lines = append(lines, dim(strings.Repeat("─", w)))
			lines = append(lines, keys...)
			if prompt != nil {
				return append(lines, prompt...)
			}
			return append(lines, " "+fit(a.status, w-1))
		}
		a.scr.draw(base(nil), -1, -1)
		k, ok := a.scr.keys.Read()
		if !ok {
			return
		}
		a.status = ""
		l := a.current()
		switch {
		case k.Name == "esc" || k.Rune == 'q' || k.Name == "left" || k.Name == "bs" || k.Name == "ctrl-c":
			return
		case k.Name == "up" || k.Rune == 'k':
			off = max(0, off-1)
		case k.Name == "down" || k.Rune == 'j':
			off++
		case k.Rune == ' ' || k.Rune == 't':
			a.toggle()
		case k.Rune == 'e' || k.Rune == 's':
			a.editSchedule(base)
		case k.Rune == 'c':
			a.editCommand(base)
		case k.Rune == 'p' || k.Rune == 'f':
			a.editPath(base)
		case k.Rune == 'R':
			a.check([]*Line{l})
			a.status = "Re-checked."
		case k.Rune >= '1' && k.Rune <= '9':
			n := int(k.Rune - '0')
			if n > len(fixes) {
				a.status = fmt.Sprintf("There is no fix [%d].", n)
				continue
			}
			a.applyFix(base, l, fixes[n-1].fix)
		}
	}
}

func (a *app) applyFix(base func([]string) []string, l *Line, f Fix) {
	if f.EditFrom != "" {
		nc, ok := ApplyEdit(l.Command, f.EditFrom, f.EditTo)
		if !ok {
			a.status = red("Could not find " + f.EditFrom + " in the command; edit it with c.")
			return
		}
		if r := a.ask(base, "Change the command to:", nc+"   (y = yes, any other key = no)"); r != 'y' && r != 'Y' {
			a.status = "Cancelled."
			return
		}
		_ = l.SetCommand(nc)
		a.check([]*Line{l})
		a.status = "Command updated (press w to save). Check: " + sevLabel(a.rep[l].Sev())
		return
	}
	disp := a.fixCmdDisplay(f)
	if r := a.ask(base, "Run on "+a.r.Label()+": "+disp, "y = run   any other key = cancel"); r != 'y' && r != 'Y' {
		a.status = "Cancelled."
		return
	}
	if f.Sudo && a.info.User != "root" {
		a.scr.suspend()
		fmt.Printf("\n%s %s\n$ %s\n\n", bold("Running on"), a.r.Label(), disp)
		err := a.r.Interactive("sudo sh -c " + shq(f.Cmd))
		if err != nil {
			fmt.Printf("\n%s %v\n", red("failed:"), err)
		} else {
			fmt.Printf("\n%s\n", green("done."))
		}
		fmt.Print("Press Enter to return to cronman…")
		_, _ = readLine("")
		a.scr.resume()
		if err != nil {
			a.status = red("Fix failed: " + err.Error())
		}
	} else {
		out, stderr, err := RunScript(a.r, "", f.Cmd+"\n")
		if err != nil {
			a.status = red("Fix failed: " + oneLine(stderr+" "+out+" "+err.Error()))
		}
	}
	a.check([]*Line{l})
	if a.status == "" {
		a.status = "Fix applied; re-checked: " + sevLabel(a.rep[l].Sev())
	}
}
