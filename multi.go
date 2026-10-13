// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"fmt"
	"sort"
	"strings"
)

// Multi-host mode: edit one job on several hosts at once. Only jobs whose
// command path (the script or executable they run) is on every host are shown.

// mrow is one job that exists on every host: lines[i] is its line on host i.
type mrow struct {
	path  string
	exact bool // matched on the whole command, not only the path
	lines []*Line
}

// skippedPath is a path found on every host but in more than one job on some.
type skippedPath struct {
	path   string
	counts []int // jobs with this path, per host
}

// jobPath returns the script/executable a job runs, as written in the command.
func jobPath(ct *Crontab, l *Line, home string) string {
	mt := MainTarget(ExtractTargets(l.Command, ct.EnvAt(l, home)).Targets)
	if mt == nil {
		return ""
	}
	return mt.Word
}

// matchJobs pairs up the jobs that exist on every host. Jobs whose whole command
// is on every host exactly once pair first; the rest pair on the command path
// when every host has exactly one job left with it. Rows follow host 0's order.
func matchJobs(cts []*Crontab, homes []string) ([]*mrow, []skippedPath) {
	n := len(cts)
	used := make([]map[*Line]bool, n)
	for i := range used {
		used[i] = map[*Line]bool{}
	}
	var rows []*mrow

	group := func(key func(i int, l *Line) string) []map[string][]*Line {
		out := make([]map[string][]*Line, n)
		for i, ct := range cts {
			out[i] = map[string][]*Line{}
			for _, l := range ct.Jobs() {
				if used[i][l] {
					continue
				}
				if k := key(i, l); k != "" {
					out[i][k] = append(out[i][k], l)
				}
			}
		}
		return out
	}
	// pair takes keys in host 0's order; it returns the keys found on every
	// host that are not unique on some host.
	pair := func(groups []map[string][]*Line, key func(i int, l *Line) string, exact bool) []skippedPath {
		var amb []skippedPath
		seen := map[string]bool{}
		for _, l0 := range cts[0].Jobs() {
			if used[0][l0] {
				continue
			}
			k := key(0, l0)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			counts := make([]int, n)
			unique, everywhere := true, true
			for i := range cts {
				counts[i] = len(groups[i][k])
				unique = unique && counts[i] == 1
				everywhere = everywhere && counts[i] > 0
			}
			if !everywhere {
				continue
			}
			if !unique {
				amb = append(amb, skippedPath{path: k, counts: counts})
				continue
			}
			r := &mrow{exact: exact}
			for i := range cts {
				l := groups[i][k][0]
				used[i][l] = true
				r.lines = append(r.lines, l)
			}
			r.path = jobPath(cts[0], r.lines[0], homes[0])
			rows = append(rows, r)
		}
		return amb
	}

	byCmd := func(i int, l *Line) string { return oneLine(l.Command) }
	pair(group(byCmd), byCmd, true)
	byPath := func(i int, l *Line) string { return jobPath(cts[i], l, homes[i]) }
	skipped := pair(group(byPath), byPath, false)

	sort.SliceStable(rows, func(a, b int) bool { return rows[a].lines[0].Num < rows[b].lines[0].Num })
	return rows, skipped
}

// ---- app -------------------------------------------------------------------

type multiApp struct {
	ui      *app   // screen and prompt helpers
	hosts   []*app // one per host: runner, crontab and check results
	rows    []*mrow
	skipped []skippedPath
	sel     int
	off     int
	status  string
}

func runMulti(runners []Runner) error {
	m := &multiApp{}
	for _, r := range runners {
		fmt.Fprintf(stderr(), "Connecting to %s…\n", r.Label())
		ri, content, err := loadRemote(r)
		if err != nil {
			fmt.Fprintf(stderr(), "%s skipping %s: %v\n", yellow("warning:"), r.Label(), err)
			continue
		}
		h := &app{r: r, info: ri}
		h.setContent(content)
		m.hosts = append(m.hosts, h)
	}
	if len(m.hosts) == 0 {
		return fmt.Errorf("no host could be loaded")
	}
	m.match()
	if len(m.rows) == 0 {
		msg := fmt.Sprintf("no cron job runs the same command path on all %d hosts", len(m.hosts))
		for _, s := range m.skipped {
			msg += "\n  " + m.skippedText(s)
		}
		return fmt.Errorf("%s", msg)
	}
	fmt.Fprintf(stderr(), "Checking %s on %d hosts…\n", plural(len(m.rows), "shared job"), len(m.hosts))
	m.checkAll()
	scr, err := openScreen()
	if err != nil {
		return err
	}
	m.ui = &app{scr: scr}
	defer scr.close()
	m.status = fmt.Sprintf("%s on all %d hosts.", plural(len(m.rows), "job"), len(m.hosts))
	if len(m.skipped) > 0 {
		m.status += " " + yellow(plural(len(m.skipped), "path")+" not shown (in several jobs on a host); see ?.")
	}
	m.status += " Press ? for help."
	m.loop()
	return nil
}

func (m *multiApp) match() {
	var cts []*Crontab
	var homes []string
	for _, h := range m.hosts {
		cts = append(cts, h.ct)
		homes = append(homes, h.info.Home)
	}
	m.rows, m.skipped = matchJobs(cts, homes)
	if m.sel >= len(m.rows) {
		m.sel = max(0, len(m.rows)-1)
	}
}

// checkAll checks the shared jobs on every host.
func (m *multiApp) checkAll() {
	for i, h := range m.hosts {
		var ls []*Line
		for _, r := range m.rows {
			ls = append(ls, r.lines[i])
		}
		h.check(ls)
	}
}

func (m *multiApp) checkRow(r *mrow) {
	for i, h := range m.hosts {
		h.check([]*Line{r.lines[i]})
	}
}

func (m *multiApp) current() *mrow {
	if m.sel < 0 || m.sel >= len(m.rows) {
		return nil
	}
	return m.rows[m.sel]
}

func (m *multiApp) dirty() bool {
	for _, h := range m.hosts {
		if h.dirty() {
			return true
		}
	}
	return false
}

func (m *multiApp) skippedText(s skippedPath) string {
	var parts []string
	for i, c := range s.counts {
		if c > 1 {
			parts = append(parts, fmt.Sprintf("%d jobs on %s", c, hostOf(m.hosts[i].r.Label())))
		}
	}
	return s.path + ": " + strings.Join(parts, ", ")
}

// common returns the value if it is the same on every host.
func (r *mrow) common(f func(*Line) string) (string, bool) {
	v := f(r.lines[0])
	for _, l := range r.lines[1:] {
		if f(l) != v {
			return v, false
		}
	}
	return v, true
}

func (r *mrow) state() string {
	on := 0
	for _, l := range r.lines {
		if !l.Disabled {
			on++
		}
	}
	switch on {
	case len(r.lines):
		return green("ON ")
	case 0:
		return dim("OFF")
	}
	return yellow("MIX")
}

func (m *multiApp) rowSev(r *mrow) Severity {
	s := SevOK
	for i, l := range r.lines {
		if v := m.hosts[i].rep[l].Sev(); v > s {
			s = v
		}
	}
	return s
}

func (r *mrow) modified() bool {
	for _, l := range r.lines {
		if l.Modified {
			return true
		}
	}
	return false
}

// ---- rendering -------------------------------------------------------------

var multiKeys = [][2]string{
	{"↑↓", "move"}, {"space", "enable/disable"}, {"e", "edit schedule"}, {"c", "edit command"},
	{"p", "change script path"}, {"w", "save"}, {"d", "diff"},
	{"r", "reload"}, {"R", "re-check"}, {"?", "help"}, {"q", "quit"},
}

func (m *multiApp) titleLine() string {
	var names []string
	for _, h := range m.hosts {
		names = append(names, h.info.User+"@"+hostOf(h.r.Label()))
	}
	w, _ := m.ui.scr.size()
	t := inv(bold(" cronman ")) + " " + bold(fit(strings.Join(names, ", "), max(20, w-60))) +
		dim(fmt.Sprintf("  %s on all %d hosts", plural(len(m.rows), "job"), len(m.hosts)))
	fail := 0
	for _, r := range m.rows {
		if m.rowSev(r) == SevFail {
			fail++
		}
	}
	if fail > 0 {
		t += "  " + red(fmt.Sprintf("%d failing", fail))
	}
	if m.dirty() {
		t += "  " + yellow("● unsaved changes (w to save)")
	}
	return t
}

func (m *multiApp) hostPanelHeight() int { return min(len(m.hosts), 6) + 1 }

func (m *multiApp) render(promptLines []string) []string {
	w, h := m.ui.scr.size()
	keys := keybar(w, multiKeys)
	sep := dim(strings.Repeat("─", w))
	panelH := m.hostPanelHeight()
	fixed := 1 + 1 + 1 + panelH + 1 + len(keys) + 1
	if promptLines != nil {
		fixed += len(promptLines) - 1
	}
	listH := max(3, h-fixed)
	lines := []string{m.titleLine()}
	schedW := 13
	for _, r := range m.rows {
		for _, l := range r.lines {
			schedW = max(schedW, min(len(l.Schedule), 24))
		}
	}
	lines = append(lines, dim(fmt.Sprintf("   %3s  %-5s %-5s %-*s %s", "#", "STATE", "CHECK", schedW, "SCHEDULE", "COMMAND")))
	if m.sel < m.off {
		m.off = m.sel
	}
	if m.sel >= m.off+listH {
		m.off = m.sel - listH + 1
	}
	for i := 0; i < listH; i++ {
		j := m.off + i
		if j >= len(m.rows) {
			lines = append(lines, "")
			continue
		}
		r := m.rows[j]
		mark := "  "
		if j == m.sel {
			mark = cyan("▶ ")
		}
		pre := fmt.Sprintf("%s%3d  %s   %s ", mark, j+1, r.state(), padRight(sevLabel(m.rowSev(r)), 5))
		s, same := r.common(func(l *Line) string { return l.Schedule })
		if !same {
			s = "(differs)"
		}
		sched := fmt.Sprintf("%-*s ", schedW, fit(s, schedW))
		rest := w - visibleLen(pre) - len(sched) - 1
		cmd, same := r.common(func(l *Line) string { return l.Command })
		if !same {
			cmd = r.path + " (arguments differ)"
		}
		cmd = fit(cmd, rest)
		if r.modified() {
			sched = yellow(sched)
		}
		body := sched + cmd
		if j == m.sel {
			body = bold(body)
		}
		lines = append(lines, pre+body)
	}
	lines = append(lines, sep)
	lines = append(lines, m.hostPanel(w)...)
	lines = append(lines, sep)
	lines = append(lines, keys...)
	if promptLines != nil {
		lines = append(lines, promptLines...)
	} else {
		lines = append(lines, " "+fit(m.status, w-1))
	}
	return lines
}

// hostPanel shows the selected job as it is on each host.
func (m *multiApp) hostPanel(w int) []string {
	n := m.hostPanelHeight()
	out := make([]string, 0, n)
	r := m.current()
	if r == nil {
		for len(out) < n {
			out = append(out, "")
		}
		return out
	}
	desc := r.path
	if s, same := r.common(func(l *Line) string { return l.Schedule }); same {
		if d := Describe(s); d != "" {
			desc += " · " + d
		}
	}
	out = append(out, dim(fit(" "+desc, w)))
	nameW := 0
	for _, h := range m.hosts {
		nameW = max(nameW, len(hostOf(h.r.Label())))
	}
	nameW = min(nameW, 24)
	shown := n - 1
	if len(m.hosts) > shown {
		shown--
	}
	for i := 0; i < shown; i++ {
		h, l := m.hosts[i], r.lines[i]
		state := green("ON ")
		if l.Disabled {
			state = dim("OFF")
		}
		line := fmt.Sprintf(" %-*s %s %s %s  %s", nameW, fit(hostOf(h.r.Label()), nameW), state,
			padRight(sevLabel(h.rep[l].Sev()), 5), l.Schedule, l.Command)
		line = fit(line, w)
		if l.Modified {
			line = yellow(line)
		}
		out = append(out, line)
	}
	if len(m.hosts) > shown {
		out = append(out, dim(fmt.Sprintf(" … and %d more hosts (d shows pending changes for all)", len(m.hosts)-shown)))
	}
	return out
}

// ---- interaction -----------------------------------------------------------

func (m *multiApp) loop() {
	for {
		m.ui.scr.draw(m.render(nil), -1, -1)
		k, ok := m.ui.scr.keys.Read()
		if !ok {
			return
		}
		m.status = ""
		switch {
		case k.Name == "up" || k.Rune == 'k':
			if m.sel > 0 {
				m.sel--
			}
		case k.Name == "down" || k.Rune == 'j':
			if m.sel < len(m.rows)-1 {
				m.sel++
			}
		case k.Name == "pgup":
			m.sel = max(0, m.sel-10)
		case k.Name == "pgdn":
			m.sel = max(0, min(len(m.rows)-1, m.sel+10))
		case k.Name == "home" || k.Rune == 'g':
			m.sel = 0
		case k.Name == "end" || k.Rune == 'G':
			m.sel = max(0, len(m.rows)-1)
		case k.Rune == ' ' || k.Rune == 't':
			m.toggle()
		case k.Rune == 'e' || k.Rune == 's':
			m.editSchedule()
		case k.Rune == 'c':
			m.editCommand()
		case k.Rune == 'p' || k.Rune == 'f':
			m.editPath()
		case k.Rune == 'x' || k.Name == "enter":
			m.status = "Run-now and fixes work on one host: run cronman with just that host."
		case k.Rune == 'w':
			m.save()
		case k.Rune == 'd':
			m.showDiff(false)
		case k.Rune == 'r':
			m.reload()
		case k.Rune == 'R':
			m.status = "Re-checking…"
			m.ui.scr.draw(m.render(nil), -1, -1)
			m.checkAll()
			m.status = "Checks refreshed."
		case k.Rune == '?' || k.Rune == 'h':
			m.help()
		case k.Rune == 'q' || k.Name == "ctrl-c" || k.Name == "esc":
			if m.confirmQuit() {
				return
			}
		}
	}
}

func (m *multiApp) toggle() {
	r := m.current()
	if r == nil {
		return
	}
	// Mixed or all on → turn off everywhere; all off → turn on everywhere.
	off := false
	for _, l := range r.lines {
		off = off || !l.Disabled
	}
	for _, l := range r.lines {
		l.SetDisabled(off)
	}
	if off {
		m.status = fmt.Sprintf("Job %d commented out on all %d hosts (press w to save).", m.sel+1, len(m.hosts))
		return
	}
	m.status = fmt.Sprintf("Job %d enabled on all %d hosts (press w to save).", m.sel+1, len(m.hosts))
	if m.rowSev(r) == SevFail {
		m.status += " " + red("Warning: its checks are failing on some host.")
	}
}

func (m *multiApp) editSchedule() {
	r := m.current()
	if r == nil {
		return
	}
	cur, same := r.common(func(l *Line) string { return l.Schedule })
	label := "New schedule for all hosts (min hour dom mon dow, or @daily/@hourly/@reboot…):"
	if !same {
		label = "Schedules differ; new schedule for all hosts:"
	}
	v, ok := m.ui.prompt(m.render, label, cur, func(s string) string {
		if err := ValidateSchedule(strings.Join(strings.Fields(s), " ")); err != nil {
			return "invalid: " + err.Error()
		}
		return ""
	})
	if !ok {
		m.status = "Cancelled."
		return
	}
	v = strings.Join(strings.Fields(v), " ")
	if same && v == cur {
		m.status = "Schedule unchanged."
		return
	}
	for _, l := range r.lines {
		if l.Schedule != v {
			_ = l.SetSchedule(v)
		}
	}
	m.status = "Schedule → " + v + " on all hosts"
	if d := Describe(v); d != "" {
		m.status += " (" + d + ")"
	}
	m.status += ". Press w to save."
}

func (m *multiApp) editCommand() {
	r := m.current()
	if r == nil {
		return
	}
	cur, same := r.common(func(l *Line) string { return l.Command })
	if !same {
		m.status = yellow("The arguments differ between hosts, so the whole command can't be set at once. Use p to change the path, or edit each host on its own.")
		return
	}
	v, ok := m.ui.prompt(m.render, "Edit command (all hosts):", cur, func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "command cannot be empty"
		}
		return ""
	})
	if !ok || strings.TrimSpace(v) == cur {
		m.status = "Command unchanged."
		return
	}
	for _, l := range r.lines {
		_ = l.SetCommand(v)
	}
	m.recheck(r, "Command")
}

func (m *multiApp) editPath() {
	r := m.current()
	if r == nil {
		return
	}
	v, ok := m.ui.prompt(m.render, "New path for "+r.path+" (all hosts):", r.path, func(s string) string {
		if strings.TrimSpace(s) == "" {
			return "path cannot be empty"
		}
		return ""
	})
	v = strings.TrimSpace(v)
	if !ok || v == r.path {
		m.status = "Path unchanged."
		return
	}
	// Work out every host's new command before changing any.
	cmds := make([]string, len(r.lines))
	for i, l := range r.lines {
		nc, found := ApplyEdit(l.Command, r.path, v)
		if !found {
			m.status = red("Could not locate " + r.path + " in the command on " + hostOf(m.hosts[i].r.Label()) + "; nothing changed.")
			return
		}
		cmds[i] = nc
	}
	for i, l := range r.lines {
		_ = l.SetCommand(cmds[i])
	}
	r.path = v
	m.recheck(r, "Path")
}

func (m *multiApp) recheck(r *mrow, what string) {
	m.status = "Re-checking on all hosts…"
	m.ui.scr.draw(m.render(nil), -1, -1)
	m.checkRow(r)
	m.status = what + " updated on all hosts and re-checked: " + sevLabel(m.rowSev(r)) + ". Press w to save."
}

func (m *multiApp) confirmQuit() bool {
	if !m.dirty() {
		return true
	}
	switch m.ui.ask(m.render, "You have unsaved changes.", "s = save and quit   d = discard and quit   esc = cancel") {
	case 's', 'S':
		return m.save()
	case 'd', 'D':
		return true
	}
	return false
}

func (m *multiApp) reload() {
	if m.dirty() {
		if k := m.ui.ask(m.render, "Reload every host and discard your unsaved changes?", "y = yes   any other key = no"); k != 'y' && k != 'Y' {
			return
		}
	}
	m.status = "Reloading…"
	m.ui.scr.draw(m.render(nil), -1, -1)
	var failed []string
	for _, h := range m.hosts {
		ri, content, err := loadRemote(h.r)
		if err != nil {
			failed = append(failed, hostOf(h.r.Label())+": "+oneLine(err.Error()))
			continue
		}
		h.info = ri
		h.setContent(content)
	}
	m.match()
	m.checkAll()
	m.status = "Reloaded: " + plural(len(m.rows), "job") + " on all hosts."
	if len(failed) > 0 {
		m.status = red("reload failed on " + strings.Join(failed, "; "))
	}
}

// diffLines lists pending changes host by host.
func (m *multiApp) diffLines() []string {
	var out []string
	for _, h := range m.hosts {
		d := h.diffLines()
		if len(d) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, bold(" "+h.info.User+"@"+hostOf(h.r.Label())))
		out = append(out, d...)
	}
	return out
}

// showDiff displays pending changes; with confirm it asks y/n and returns the answer.
func (m *multiApp) showDiff(confirm bool) bool {
	n := 0
	for _, h := range m.hosts {
		if h.dirty() {
			n++
		}
	}
	return m.page(" Pending changes ", m.diffLines(), confirm, "Write these crontabs to "+plural(n, "host")+"?", "No changes.")
}

// page shows a scrollable list of lines.
func (m *multiApp) page(title string, d []string, confirm bool, question, empty string) bool {
	if len(d) == 0 {
		m.status = empty
		return false
	}
	off := 0
	for {
		w, h := m.ui.scr.size()
		lines := []string{inv(bold(title)), ""}
		body := h - 5
		for i := off; i < len(d) && i < off+body; i++ {
			lines = append(lines, fit(d[i], w+20))
		}
		for len(lines) < h-2 {
			lines = append(lines, "")
		}
		if confirm {
			lines = append(lines, " "+bold(question), " "+key("y")+" save   "+key("n/esc")+" cancel   "+key("↑↓")+" scroll")
		} else {
			lines = append(lines, "", " "+key("↑↓")+" scroll   "+key("esc/q")+" back")
		}
		m.ui.scr.draw(lines, -1, -1)
		k, ok := m.ui.scr.keys.Read()
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
		case k.Rune == 'n' || k.Rune == 'q' || k.Name == "esc" || k.Name == "ctrl-c" || k.Name == "enter":
			return false
		}
	}
}

// save writes every changed host. A host that fails or was changed meanwhile
// keeps its edits; the others are saved.
func (m *multiApp) save() bool {
	if !m.dirty() {
		m.status = "Nothing to save."
		return true
	}
	if !m.showDiff(true) {
		m.status = "Save cancelled."
		return false
	}
	var report []string
	saved, failed := 0, 0
	written := map[*app]string{} // reloaded once every host is done
	for _, h := range m.hosts {
		if !h.dirty() {
			continue
		}
		name := h.info.User + "@" + hostOf(h.r.Label())
		m.status = "Saving " + name + "…"
		m.ui.scr.draw(m.render(nil), -1, -1)
		backup, verify, err := writeCrontab(h.r, h.orig, h.ct.Render())
		switch {
		case err == errChangedMeanwhile:
			failed++
			report = append(report, " "+red("SKIPPED")+" "+name+": "+err.Error()+"; your edits for it are kept (r reloads and drops them)")
		case err == errReadBack:
			saved++
			report = append(report, " "+yellow("SAVED?")+"  "+name+": "+err.Error())
			written[h] = verify
		case err != nil:
			failed++
			report = append(report, " "+red("FAILED")+"  "+name+": "+oneLine(err.Error())+"; your edits for it are kept")
		default:
			saved++
			line := " " + green("OK") + "      " + name
			if backup != "" {
				line += dim("  backup: " + backup)
			}
			report = append(report, line)
			written[h] = verify
		}
	}
	for h, content := range written {
		h.setContent(content)
	}
	m.match()
	m.checkAll()
	for _, r := range m.rows {
		for i, l := range r.lines {
			if m.hosts[i].rep[l].Sev() == SevFail && !l.Disabled {
				report = append(report, " "+red("FAIL")+"    "+hostOf(m.hosts[i].r.Label())+": "+fit(l.Command, 60)+" — its checks fail there")
			}
		}
	}
	m.page(" Save results ", report, false, "", "")
	if failed > 0 {
		m.status = red(fmt.Sprintf("Saved %s, %d not saved.", plural(saved, "host"), failed))
		return false
	}
	m.status = green("Saved " + plural(saved, "host") + ".")
	return true
}

func (m *multiApp) help() {
	lines := []string{
		inv(bold(" cronman — several hosts ")),
		"",
		" Only jobs whose command path (the script or executable they run) is on every host are listed.",
		" Edits apply to that job on every host; each host keeps its own arguments.",
		"",
		"   " + key("↑ ↓  j k") + "        move selection",
		"   " + key("space  t") + "        comment out on every host (or enable, when it is off everywhere)",
		"   " + key("e") + "               set the schedule on every host",
		"   " + key("c") + "               edit the whole command (only when it is the same on every host)",
		"   " + key("p") + "               change the script/executable path on every host",
		"   " + key("w") + "               save: one diff for all hosts, then each host is backed up and written",
		"   " + key("d") + "               show pending changes     " + key("r") + "  reload all     " + key("R") + "  re-check",
		"   " + key("q  esc") + "          quit (asks if there are unsaved changes)",
		"",
		" STATE " + yellow("MIX") + " means the job is on for some hosts and off for others.",
		" A host whose crontab changed since it was loaded is skipped on save; the others are saved.",
	}
	if len(m.skipped) > 0 {
		lines = append(lines, "", bold(" Not listed (in more than one job on a host; edit those hosts one at a time)"))
		for _, s := range m.skipped {
			lines = append(lines, "   "+m.skippedText(s))
		}
	}
	lines = append(lines, "", " press any key to return")
	m.ui.scr.draw(lines, -1, -1)
	m.ui.scr.keys.Read()
}
