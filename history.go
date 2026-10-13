// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Last-run history: when each job last ran and whether it worked. Two sources:
//
//   - the cron log (read-only). Every flavor logs the start of a job; Debian
//     cron with -L 4 also logs failures with their exit status, cronie logs the
//     end (CMDEND) and output (CMDOUT) but no exit status, busybox only the start.
//   - status files written by an opt-in wrapper (S in the TUI), which records
//     start, end and exit status of every run in ~/.cronman/status/ID.

// RunResult is what is known about how a run ended.
type RunResult int

const (
	RunUnknown RunResult = iota // started; the log does not say how it ended
	RunOK
	RunFailed
)

// Run is one execution of a job.
type Run struct {
	Start  time.Time
	End    time.Time // zero if not known
	Result RunResult
	Exit   int      // exit status when Result is RunOK or RunFailed
	Output []string // output cronie logged with -s (CMDOUT)
	pid    string   // syslog pid of the log line, to link END/failure lines
	ended  bool
}

// JobHistory is what is known about the recent runs of one job.
type JobHistory struct {
	Runs    []*Run      // oldest first
	Running []string    // pids of the job's processes running now
	Missed  []time.Time // scheduled starts with no run, since the job was first seen
	LogOK   bool        // the cron log was read: no runs means it did not run
	Wrapped bool        // status recording is on for this job
}

// Last returns the most recent run, or nil.
func (h *JobHistory) Last() *Run {
	if h == nil || len(h.Runs) == 0 {
		return nil
	}
	return h.Runs[len(h.Runs)-1]
}

// LastWith returns the most recent run with result res, or nil.
func (h *JobHistory) LastWith(res RunResult) *Run {
	if h == nil {
		return nil
	}
	for i := len(h.Runs) - 1; i >= 0; i-- {
		if h.Runs[i].Result == res {
			return h.Runs[i]
		}
	}
	return nil
}

// MissedLast reports whether the most recent scheduled start was missed.
func (h *JobHistory) MissedLast() bool {
	if h == nil || len(h.Missed) == 0 {
		return false
	}
	last := h.Last()
	return last == nil || h.Missed[len(h.Missed)-1].After(last.Start)
}

// Sev rates the history: a failed or missed last run is a warning.
func (h *JobHistory) Sev() Severity {
	if h == nil {
		return SevOK
	}
	if l := h.Last(); l != nil && l.Result == RunFailed && len(h.Running) == 0 {
		return SevWarn
	}
	if h.MissedLast() {
		return SevWarn
	}
	return SevOK
}

// ---- status wrapper ----------------------------------------------------------

const statusDir = ".cronman/status"

var (
	wrapRe     = regexp.MustCompile(`^cronman_t=\$\(date \+\\%s\); (.*); cronman_rc=\$\?; echo "\$cronman_t \$\(date \+\\%s\) \$cronman_rc" 2>/dev/null >>"\$HOME/\.cronman/status/([0-9a-f]+)"; exit \$cronman_rc(.*)$`)
	commentRe  = regexp.MustCompile(`(^|[ \t;])#`)
	statusIDRe = regexp.MustCompile(`^[0-9a-f]{1,32}$`)
)

// UnwrapStatus returns the job's own command if it has the status wrapper.
func UnwrapStatus(cmd string) (inner, id string, ok bool) {
	m := wrapRe.FindStringSubmatch(cmd)
	if m == nil {
		return cmd, "", false
	}
	return m[1] + m[3], m[2], true
}

// innerCommand is the command without the status wrapper.
func innerCommand(cmd string) string {
	inner, _, _ := UnwrapStatus(cmd)
	return inner
}

// WrapStatus adds the status wrapper to a cron command: it records the start
// time, end time and exit status of every run in ~/.cronman/status/ID and keeps
// the job's exit status. Text after '%' (stdin) stays at the end.
func WrapStatus(cmd, id string) (string, error) {
	if _, _, ok := UnwrapStatus(cmd); ok {
		return cmd, nil
	}
	if !statusIDRe.MatchString(id) {
		return "", fmt.Errorf("bad status id %q", id)
	}
	shell, stdin := cmd, ""
	if i := cronPercent(cmd); i >= 0 {
		shell, stdin = cmd[:i], cmd[i:]
	}
	shell = strings.TrimSpace(shell)
	for strings.HasSuffix(shell, ";") {
		shell = strings.TrimSpace(strings.TrimSuffix(shell, ";"))
	}
	switch {
	case shell == "":
		return "", fmt.Errorf("empty command")
	case strings.HasSuffix(shell, "&"):
		return "", fmt.Errorf("the command ends with & (runs in the background), so its result cannot be recorded")
	case commentRe.MatchString(shell):
		return "", fmt.Errorf("the command contains a # comment, which would hide the recording part")
	}
	return `cronman_t=$(date +\%s); ` + shell + `; cronman_rc=$?; echo "$cronman_t $(date +\%s) $cronman_rc" 2>/dev/null >>"$HOME/.cronman/status/` + id + `"; exit $cronman_rc` + stdin, nil
}

// cronPercent returns the index of the first unescaped '%' (where cron's stdin
// part starts), or -1.
func cronPercent(cmd string) int {
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == '\\' && i+1 < len(cmd) && cmd[i+1] == '%' {
			i++
			continue
		}
		if cmd[i] == '%' {
			return i
		}
	}
	return -1
}

// ---- reading the host ----------------------------------------------------------

// nowScript prints the host's time, read back by parseNow.
const nowScript = `printf '@@NOW %s\n' "$(date '+%Y-%m-%d %H:%M:%S %z')"` + "\n"

func parseNow(out string) (time.Time, bool) {
	v, ok := strings.CutPrefix(strings.SplitN(out, "\n", 2)[0], "@@NOW ")
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02 15:04:05 -0700", v)
	return t, err == nil
}

// logScript prints the cron lines of the cron log (only user's, unless user is
// empty). Files come first; the journal and logread only if no file had any.
// "@@SRC name" starts a source with cron lines; "@@NOREAD name" is a source
// that exists but cannot be read.
func logScript(user string) string {
	return `u=` + shq(user) + `
re='\) (CMD|CMDEND|END|CMDOUT) \(|grandchild #|: USER [^ ]+ pid [0-9]+ cmd '
t="${TMPDIR:-/tmp}/.cronman-h.$$"
found=
filt() { if [ -n "$u" ]; then grep -F -e "($u) " -e "USER $u pid " -e "grandchild #"; else cat; fi; }
src() { grep -E "$re" >"$t"; if [ -s "$t" ]; then found=1; echo "@@SRC $1"; filt <"$t"; fi; }
for f in /var/log/cron /var/log/syslog /var/log/messages; do
  [ -f "$f" ] || continue
  if [ ! -r "$f" ]; then echo "@@NOREAD $f"; continue; fi
  tail -n 200000 "$f" >"$t.raw" 2>/dev/null; src "$f" <"$t.raw"
done
if [ -z "$found" ] && command -v journalctl >/dev/null 2>&1; then
  journalctl -q --no-pager -o short-iso --since "-8 days" -t CRON -t CROND -t crond -t cron >"$t.raw" 2>/dev/null
  src journal <"$t.raw"
  if [ -z "$found" ] && [ "$(id -u)" != 0 ] && ! id -Gn | tr ' ' '\n' | grep -qxE 'adm|systemd-journal|wheel'; then echo "@@NOREAD journal"; fi
fi
if [ -z "$found" ] && command -v logread >/dev/null 2>&1; then
  if logread >"$t.raw" 2>/dev/null; then src logread <"$t.raw"; else echo "@@NOREAD logread"; fi
fi
rm -f "$t" "$t.raw"
`
}

// stateScript prints the status files under each user's home and the running
// processes. Files over 2000 lines are cut to the last 1000 (by their owner only,
// so they stay writable for the job).
func stateScript(homes map[string]string) string {
	users := make([]string, 0, len(homes))
	for u := range homes {
		users = append(users, u)
	}
	sort.Strings(users)
	var b strings.Builder
	for _, u := range users {
		b.WriteString(`d=` + shq(homes[u]+"/"+statusDir) + `
echo @@USER ` + shq(u) + `
for f in "$d"/*; do
  [ -f "$f" ] || continue
  case "${f##*/}" in *[!0-9a-f]*) continue ;; esac
  if [ -O "$f" ] && [ "$(wc -l <"$f")" -gt 2000 ]; then tail -n 1000 "$f" >"$f.tmp" && cat "$f.tmp" >"$f"; rm -f "$f.tmp"; fi
  echo "@@STATUS ${f##*/}"; tail -n 300 "$f"
done
`)
	}
	b.WriteString(`echo "@@PS"
ps -eo pid=,user=,args= 2>/dev/null || ps -o pid,user,args
`)
	return b.String()
}

type logEvent struct {
	t    time.Time
	kind string // CMD, END, CMDEND, CMDOUT, FAIL
	user string
	cmd  string // command text, or output for CMDOUT
	pid  string // syslog pid
	exit int    // FAIL only
}

type statusRec struct {
	start, end time.Time
	exit       int
}

type procInfo struct{ pid, user, args string }

// hostRuns is everything read from one host.
type hostRuns struct {
	events   []logEvent
	status   map[string]map[string][]statusRec // user -> id -> records
	procs    []procInfo
	sources  []string
	noRead   []string
	logLevel int // Debian cron -L value, -1 if not given
	now      time.Time
}

var (
	isoTSRe  = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d`)
	tagRe    = regexp.MustCompile(`^\S+\s+(?:[a-z]+\.[a-z]+\s+)?([A-Za-z_.-]+)\[(\d+)\]:\s(.*)$`)
	cronMsg  = regexp.MustCompile(`^\(([^)]*)\) (CMD|END|CMDEND|CMDOUT) \((.*)\)$`)
	failMsg  = regexp.MustCompile(`^\(CRON\) error \(grandchild #\d+ failed with exit status (\d+)\)`)
	bbMsg    = regexp.MustCompile(`^USER (\S+) pid +\d+ cmd (.*)$`)
	pidPfxRe = regexp.MustCompile(`^\[\d+\] `)
	logLvlRe = regexp.MustCompile(`(?:^|\s)-L\s*(\d+)`)
)

// parseLogTime parses an ISO (journal short-iso, rsyslog RFC 3339) or a
// traditional syslog timestamp; it returns the rest of the line.
func parseLogTime(line string, now time.Time) (time.Time, string, bool) {
	if isoTSRe.MatchString(line) {
		ts, rest, _ := strings.Cut(line, " ")
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05.999999-0700"} {
			if t, err := time.Parse(layout, ts); err == nil {
				return t, rest, true
			}
		}
		return time.Time{}, "", false
	}
	if len(line) < 16 {
		return time.Time{}, "", false
	}
	t, err := time.ParseInLocation("Jan _2 15:04:05", line[:15], now.Location())
	if err != nil {
		return time.Time{}, "", false
	}
	t = t.AddDate(now.Year(), 0, 0)
	if t.After(now.Add(24 * time.Hour)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, line[16:], true
}

// parseLogLine turns one cron log line into an event.
func parseLogLine(line string, now time.Time) (logEvent, bool) {
	t, rest, ok := parseLogTime(line, now)
	if !ok {
		return logEvent{}, false
	}
	m := tagRe.FindStringSubmatch(rest)
	if m == nil {
		return logEvent{}, false
	}
	ev := logEvent{t: t, pid: m[2]}
	msg := m[3]
	switch {
	case cronMsg.MatchString(msg):
		c := cronMsg.FindStringSubmatch(msg)
		ev.user, ev.kind, ev.cmd = c[1], c[2], c[3]
		if ev.kind != "CMDOUT" {
			ev.cmd = pidPfxRe.ReplaceAllString(ev.cmd, "")
		}
	case failMsg.MatchString(msg):
		ev.kind = "FAIL"
		ev.exit, _ = strconv.Atoi(failMsg.FindStringSubmatch(msg)[1])
	case bbMsg.MatchString(msg):
		c := bbMsg.FindStringSubmatch(msg)
		ev.kind, ev.user, ev.cmd = "CMD", c[1], c[2]
	default:
		return logEvent{}, false
	}
	return ev, true
}

// parseHostRuns parses the output of logScript followed by stateScript.
func parseHostRuns(out string, now time.Time) *hostRuns {
	h := &hostRuns{status: map[string]map[string][]statusRec{}, now: now, logLevel: -1}
	section, user, id := "", "", ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "@@SRC "):
			section = "log"
			h.sources = append(h.sources, strings.TrimPrefix(line, "@@SRC "))
			continue
		case strings.HasPrefix(line, "@@NOREAD "):
			h.noRead = append(h.noRead, strings.TrimPrefix(line, "@@NOREAD "))
			continue
		case strings.HasPrefix(line, "@@USER "):
			section, user = "", strings.TrimPrefix(line, "@@USER ")
			continue
		case strings.HasPrefix(line, "@@STATUS "):
			section, id = "status", strings.TrimPrefix(line, "@@STATUS ")
			if h.status[user] == nil {
				h.status[user] = map[string][]statusRec{}
			}
			continue
		case line == "@@PS":
			section = "ps"
			continue
		}
		switch section {
		case "log":
			if ev, ok := parseLogLine(line, now); ok {
				h.events = append(h.events, ev)
			}
		case "status":
			f := strings.Fields(line)
			if len(f) != 3 {
				continue
			}
			s, err1 := strconv.ParseInt(f[0], 10, 64)
			e, err2 := strconv.ParseInt(f[1], 10, 64)
			rc, err3 := strconv.Atoi(f[2])
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			h.status[user][id] = append(h.status[user][id], statusRec{time.Unix(s, 0).In(now.Location()), time.Unix(e, 0).In(now.Location()), rc})
		case "ps":
			f := strings.Fields(line)
			if len(f) < 3 || !isDigits(f[0]) {
				continue
			}
			args := strings.Join(f[2:], " ")
			h.procs = append(h.procs, procInfo{f[0], f[1], args})
			if b := path.Base(f[2]); b == "cron" || b == "crond" {
				if m := logLvlRe.FindStringSubmatch(args); m != nil {
					h.logLevel, _ = strconv.Atoi(m[1])
				}
			}
		}
	}
	sort.SliceStable(h.events, func(a, b int) bool { return h.events[a].t.Before(h.events[b].t) })
	return h
}

// merge adds the log events of another read (e.g. through sudo).
func (h *hostRuns) mergeLog(o *hostRuns) {
	h.events = append(h.events, o.events...)
	sort.SliceStable(h.events, func(a, b int) bool { return h.events[a].t.Before(h.events[b].t) })
	h.sources = append(h.sources, o.sources...)
	h.noRead = nil
}

func (h *hostRuns) logOK() bool { return len(h.sources) > 0 }

// logNote says where the history came from, for the details screen.
func (h *hostRuns) logNote(user string) string {
	if h == nil {
		return ""
	}
	if h.logOK() {
		s := "cron log: " + strings.Join(h.sources, ", ")
		if h.logLevel >= 0 && h.logLevel&4 != 0 {
			s += fmt.Sprintf(" (cron -L %d logs failures)", h.logLevel)
		}
		return s
	}
	if len(h.noRead) > 0 {
		return "cron log not readable as " + user + " (" + strings.Join(h.noRead, ", ") + ")"
	}
	return "no cron log found"
}

// cmdMatches reports whether a logged command is this job's command. Cron logs
// the command as written; some versions cut long commands short.
func cmdMatches(logged, cmd string) bool {
	logged, cmd = strings.TrimSpace(logged), strings.TrimSpace(cmd)
	if logged == cmd || logged == strings.TrimSpace(CronShellPart(cmd)) {
		return true
	}
	return len(logged) >= 40 && strings.HasPrefix(cmd, logged)
}

func (s *schedule) matches(t time.Time) bool {
	return !s.reboot && s.sets[0][t.Minute()] && s.sets[1][t.Hour()] && s.sets[3][int(t.Month())] && s.dayMatches(t)
}

// Histories works out the run history of jobs that run as user.
func (h *hostRuns) Histories(user string, jobs []*Line) map[*Line]*JobHistory {
	out := map[*Line]*JobHistory{}
	if h == nil {
		return out
	}
	scheds := map[*Line]*schedule{}
	for _, l := range jobs {
		_, _, wrapped := UnwrapStatus(l.Command)
		out[l] = &JobHistory{LogOK: h.logOK(), Wrapped: wrapped}
		if s, err := compileSchedule(l.Schedule); err == nil {
			scheds[l] = s
		}
	}
	// owner picks the job a logged command belongs to; with several jobs
	// running the same command, the one whose schedule matches the start.
	owner := func(cmd string, t time.Time) *Line {
		var cands []*Line
		for _, l := range jobs {
			if cmdMatches(cmd, l.Command) {
				cands = append(cands, l)
			}
		}
		if len(cands) > 1 {
			for _, l := range cands {
				if s := scheds[l]; s != nil && s.matches(t) {
					return l
				}
			}
		}
		if len(cands) > 0 {
			return cands[0]
		}
		return nil
	}

	byPid := map[string]*Run{}      // last run started by a syslog pid
	output := map[string][]string{} // cronie CMDOUT lines per pid, until CMDEND
	for _, ev := range h.events {
		if ev.user != "" && ev.user != user {
			continue
		}
		switch ev.kind {
		case "CMD":
			l := owner(ev.cmd, ev.t)
			if l == nil {
				continue
			}
			r := &Run{Start: ev.t, pid: ev.pid}
			out[l].Runs = append(out[l].Runs, r)
			byPid[ev.pid] = r
		case "END": // Debian: same pid as the CMD line
			if r := byPid[ev.pid]; r != nil {
				r.End, r.ended = ev.t, true
			}
		case "FAIL": // Debian -L 4: same pid as the CMD line
			if r := byPid[ev.pid]; r != nil {
				r.Result, r.Exit = RunFailed, ev.exit
			}
		case "CMDOUT":
			output[ev.pid] = append(output[ev.pid], ev.cmd)
		case "CMDEND": // cronie: another pid; take the oldest open run
			l := owner(ev.cmd, ev.t)
			if l == nil {
				continue
			}
			for _, r := range out[l].Runs {
				if !r.ended && !r.Start.After(ev.t) {
					r.End, r.ended = ev.t, true
					r.Output = output[ev.pid]
					break
				}
			}
			delete(output, ev.pid)
		}
	}

	for _, l := range jobs {
		jh := out[l]
		jh.Running = h.running(user, l.Command)
		// Debian -L with bit 4 logs every failure, so a run without one worked
		// once it is over. With bit 2 the END line says it is over; runs without
		// one were logged before the level was raised.
		if h.logLevel >= 0 && h.logLevel&4 != 0 {
			for i, r := range jh.Runs {
				over := r.ended || i < len(jh.Runs)-1 || len(jh.Running) == 0
				if h.logLevel&2 != 0 {
					over = r.ended
				}
				if r.Result == RunUnknown && over {
					r.Result = RunOK
				}
			}
		}
		if _, id, ok := UnwrapStatus(l.Command); ok {
			jh.addStatus(h.status[user][id])
		}
		if jh.LogOK && !l.Disabled && !l.Modified && scheds[l] != nil {
			jh.Missed = missedRuns(l.Schedule, jh.Runs, h.now)
		}
	}
	return out
}

// addStatus merges the wrapper's records: a log run that started within a
// minute and a half of a record gets its result; other records become runs.
func (jh *JobHistory) addStatus(recs []statusRec) {
	for _, rec := range recs {
		var match *Run
		for _, r := range jh.Runs {
			if d := r.Start.Sub(rec.start); d > -90*time.Second && d < 90*time.Second {
				match = r
				break
			}
		}
		if match == nil {
			match = &Run{Start: rec.start}
			jh.Runs = append(jh.Runs, match)
		}
		match.End, match.ended, match.Exit = rec.end, true, rec.exit
		match.Result = RunOK
		if rec.exit != 0 {
			match.Result = RunFailed
		}
	}
	sort.SliceStable(jh.Runs, func(a, b int) bool { return jh.Runs[a].Start.Before(jh.Runs[b].Start) })
}

// running returns the pids of "$SHELL -c CMD" processes of this job.
func (h *hostRuns) running(user, cmd string) []string {
	want := " -c " + strings.TrimSpace(CronShellPart(cmd))
	var pids []string
	for _, p := range h.procs {
		// ps cuts long user names (procps adds a '+').
		pu := strings.TrimSuffix(p.user, "+")
		if (pu == user || (len(pu) >= 7 && strings.HasPrefix(user, pu))) && strings.HasSuffix(p.args, want) {
			pids = append(pids, p.pid)
		}
	}
	return pids
}

// missedRuns lists the scheduled starts since the first logged run that have
// no run before the next scheduled start. The last minute is not judged yet.
func missedRuns(sched string, runs []*Run, now time.Time) []time.Time {
	if len(runs) == 0 {
		return nil
	}
	from := runs[0].Start
	until := now.Add(-time.Minute)
	exp, err := NextRuns(sched, from, 20000)
	if err != nil {
		return nil
	}
	var missed []time.Time
	j := 0
	for i, e := range exp {
		if e.After(until) {
			break
		}
		end := now
		if i+1 < len(exp) && exp[i+1].Before(now) {
			end = exp[i+1]
		}
		for j < len(runs) && runs[j].Start.Before(e) {
			j++
		}
		if j >= len(runs) || !runs[j].Start.Before(end) {
			missed = append(missed, e)
		}
	}
	return missed
}

// ---- display -------------------------------------------------------------------

// ago formats how long before now t was, in one short unit.
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// agoText is "5m ago", or "just now".
func agoText(t, now time.Time) string {
	if a := ago(t, now); a != "now" {
		return a + " ago"
	}
	return "just now"
}

// lastCell is the LAST column: the newest run and how it went.
func (jh *JobHistory) lastCell(now time.Time, logOK bool) string {
	if jh == nil {
		return dim("?")
	}
	if len(jh.Running) > 0 {
		return cyan("▶ running")
	}
	if jh.MissedLast() {
		return yellow("MISSED")
	}
	r := jh.Last()
	if r == nil {
		if logOK || jh.Wrapped {
			return dim("never")
		}
		return dim("?")
	}
	a := ago(r.Start, now)
	switch r.Result {
	case RunOK:
		return green("✓ " + a)
	case RunFailed:
		return red(fmt.Sprintf("✗ %s rc=%d", a, r.Exit))
	}
	return "• " + a
}

// strip shows the last n runs oldest first: ✓ worked, ✗ failed, • ran.
func (jh *JobHistory) strip(n int) string {
	if jh == nil {
		return ""
	}
	runs := jh.Runs
	if len(runs) > n {
		runs = runs[len(runs)-n:]
	}
	var b strings.Builder
	for _, r := range runs {
		switch r.Result {
		case RunOK:
			b.WriteString(green("✓"))
		case RunFailed:
			b.WriteString(red("✗"))
		default:
			b.WriteString("•")
		}
	}
	return b.String()
}

// historyLines describes a job's runs for the details screen.
func (jh *JobHistory) historyLines(now time.Time, note string) []string {
	var out []string
	out = append(out, " "+bold("Last runs")+dim(" ("+note+")"))
	if jh == nil {
		return append(out, "   "+dim("not loaded"))
	}
	when := func(r *Run) string {
		s := r.Start.In(now.Location()).Format("Mon 01-02 15:04") + dim(" ("+agoText(r.Start, now)+")")
		if !r.End.IsZero() {
			s += dim(", took " + r.End.Sub(r.Start).Round(time.Second).String())
		}
		return s
	}
	for _, pid := range jh.Running {
		out = append(out, "   "+cyan("▶ running now")+dim(" (pid "+pid+")"))
	}
	if r := jh.Last(); r != nil {
		res := "result not in the log"
		switch r.Result {
		case RunOK:
			res = green("exit 0")
		case RunFailed:
			res = red(fmt.Sprintf("exit %d", r.Exit))
		}
		out = append(out, "   last run       "+when(r)+"  "+res)
	} else if jh.LogOK || jh.Wrapped {
		out = append(out, "   last run       "+dim("none recorded"))
	}
	if r := jh.LastWith(RunOK); r != nil {
		out = append(out, "   last success   "+when(r))
	}
	if r := jh.LastWith(RunFailed); r != nil {
		out = append(out, "   last failure   "+when(r)+"  "+red(fmt.Sprintf("exit %d", r.Exit)))
	}
	if len(jh.Runs) > 0 {
		out = append(out, "   recent         "+jh.strip(30)+dim("  ("+plural(len(jh.Runs), "run")+" in the log window, oldest first)"))
	}
	if len(jh.Missed) > 0 {
		m := jh.Missed[len(jh.Missed)-1]
		out = append(out, "   "+yellow(fmt.Sprintf("missed         %d scheduled run(s) did not start; latest %s", len(jh.Missed), m.In(now.Location()).Format("Mon 01-02 15:04"))))
	}
	if r := jh.Last(); r != nil && len(r.Output) > 0 {
		out = append(out, "   output (log)   "+fit(strings.Join(r.Output, " ⏎ "), 200))
	}
	if !jh.Wrapped && jh.LastWith(RunOK) == nil && jh.LastWith(RunFailed) == nil {
		out = append(out, "   "+dim("The log does not say whether runs worked. Press S to record exit status (changes the command)."))
	}
	return out
}
