// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"fmt"
	"strings"
	"time"
)

// loadHistory reads the cron log, the status files and the running processes.
// When the log cannot be read as this user it tries "sudo -n" (no prompt).
func (a *app) loadHistory() {
	a.histAt = time.Now()
	script := nowScript + logScript(a.info.User) + stateScript(map[string]string{a.info.User: a.info.Home})
	out, _, err := RunScript(a.r, "", script)
	if err != nil && !strings.Contains(out, "@@PS") {
		a.runs = nil
		a.refreshHist()
		return
	}
	if t, ok := parseNow(out); ok {
		a.info.Now = t
	}
	h := parseHostRuns(out, a.info.Now)
	if !h.logOK() && len(h.noRead) > 0 && a.info.User != "root" {
		if o, _, err := RunScript(a.r, "sudo -n", logScript(a.info.User)); err == nil {
			if s := parseHostRuns(o, a.info.Now); s.logOK() {
				h.mergeLog(s)
			}
		}
	}
	a.runs = h
	a.refreshHist()
}

// refreshHist re-assigns the loaded runs to the current jobs.
func (a *app) refreshHist() { a.hist = a.runs.Histories(a.info.User, a.jobs) }

// now is the host's time now.
func (a *app) now() time.Time { return a.info.Now.Add(time.Since(a.histAt)) }

func (a *app) logOK() bool { return a.runs != nil && a.runs.logOK() }

// histHint is a status-line note when the log could not be read.
func (a *app) histHint() string {
	if a.runs == nil || a.logOK() {
		return ""
	}
	return " " + yellow(a.runs.logNote(a.info.User)+": LAST shows only jobs with S (result recording).")
}

// displayCmd is the command as shown in lists: without the status wrapper.
func displayCmd(l *Line) string {
	if inner, _, ok := UnwrapStatus(l.Command); ok {
		return dim("[rec] ") + inner
	}
	return l.Command
}

// toggleRecording adds or removes the status wrapper on the selected job.
func (a *app) toggleRecording() {
	l := a.live()
	if l == nil {
		return
	}
	if inner, _, ok := UnwrapStatus(l.Command); ok {
		_ = l.SetCommand(inner)
		a.refreshHist()
		a.status = fmt.Sprintf("Job %d: result recording removed (press w to save).", a.sel+1)
		return
	}
	w, err := WrapStatus(l.Command, randHex(4))
	if err != nil {
		a.status = red("Can't record this job's result: " + err.Error())
		return
	}
	if err := l.SetCommand(w); err != nil {
		a.status = red(err.Error())
		return
	}
	a.refreshHist()
	a.status = fmt.Sprintf("Job %d will record start, end and exit status in ~/%s (press w to save; d shows the new line).", a.sel+1, statusDir)
}

// outputScript prints the end of each file the job writes its output to and
// the user's mailbox (cron mails output there).
func outputScript(files []string) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(`f=` + shq(f) + `
echo "@@FILE $f"
if [ -r "$f" ]; then ls -l "$f"; tail -n 200 "$f"; else echo "(cannot read)"; fi
`)
	}
	b.WriteString(`for m in "/var/mail/$(id -un)" "/var/spool/mail/$(id -un)"; do
  [ -r "$m" ] || continue
  echo "@@MAIL $m"; tail -c 400000 "$m"; break
done
`)
	return b.String()
}

// lastMail returns the last cron mail about cmd in an mbox text.
func lastMail(mbox, cmd string) (head string, body []string) {
	want := strings.TrimSpace(CronShellPart(innerCommand(cmd)))
	if len(want) > 40 {
		want = want[:40]
	}
	var msgs [][]string
	for _, ln := range strings.Split(mbox, "\n") {
		if strings.HasPrefix(ln, "From ") || msgs == nil {
			msgs = append(msgs, nil)
		}
		msgs[len(msgs)-1] = append(msgs[len(msgs)-1], ln)
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		var subj, date string
		j := 0
		for ; j < len(msgs[i]) && msgs[i][j] != ""; j++ {
			if v, ok := strings.CutPrefix(msgs[i][j], "Subject: "); ok {
				subj = v
			}
			if v, ok := strings.CutPrefix(msgs[i][j], "Date: "); ok {
				date = v
			}
		}
		if strings.HasPrefix(subj, "Cron <") && strings.Contains(subj, want) {
			return date, msgs[i][min(j+1, len(msgs[i])):]
		}
	}
	return "", nil
}

// showOutput shows the job's latest output: the files it redirects to, the
// output cronie logged, and the last cron mail about it.
func (a *app) showOutput() {
	l := a.current()
	if l == nil {
		return
	}
	var files []string
	for _, t := range ExtractTargets(innerCommand(l.Command), a.ct.EnvAt(l, a.info.Home)).Targets {
		if t.Role == "output" && t.Kind == TWrite && t.Path != "/dev/null" && !containsStr(files, t.Path) {
			files = append(files, t.Path)
		}
	}
	a.status = "Reading output…"
	a.scr.draw(a.render(nil), -1, -1)
	out, _, _ := RunScript(a.r, "", outputScript(files))
	var lines []string
	mail := ""
	for _, part := range strings.Split(out, "\n@@") {
		part = strings.TrimPrefix(part, "@@")
		switch {
		case strings.HasPrefix(part, "FILE "):
			name, rest, _ := strings.Cut(strings.TrimPrefix(part, "FILE "), "\n")
			lines = append(lines, bold(" "+name)+dim("  (output file, last 200 lines)"))
			for _, x := range strings.Split(strings.TrimRight(rest, "\n"), "\n") {
				lines = append(lines, "   "+x)
			}
			lines = append(lines, "")
		case strings.HasPrefix(part, "MAIL "):
			_, mail, _ = strings.Cut(part, "\n")
		}
	}
	if jh := a.hist[l]; jh != nil {
		if r := jh.Last(); r != nil && len(r.Output) > 0 {
			lines = append(lines, bold(" Logged output of the last run")+dim("  ("+r.Start.Format("Mon 01-02 15:04")+", cronie -s)"))
			for _, x := range r.Output {
				lines = append(lines, "   "+x)
			}
			lines = append(lines, "")
		}
	}
	if date, body := lastMail(mail, l.Command); body != nil {
		lines = append(lines, bold(" Last cron mail")+dim("  ("+date+")"))
		for _, x := range body {
			lines = append(lines, "   "+x)
		}
	}
	a.status = ""
	if len(lines) == 0 {
		a.status = "No output found: the job has no output file, no cron mail about it in the mailbox, and no output in the log."
		return
	}
	a.page(fmt.Sprintf(" Output of job %d ", a.sel+1), lines, true)
}

// page shows lines in a scrollable view until esc/q, starting at the end
// when fromEnd is set (newest output is last).
func (a *app) page(title string, d []string, fromEnd bool) {
	off := 0
	for first := true; ; first = false {
		w, h := a.scr.size()
		body := h - 4
		if first && fromEnd {
			off = max(0, len(d)-body)
		}
		lines := []string{inv(bold(title)) + " " + dim(a.r.Label()), ""}
		for i := off; i < len(d) && i < off+body; i++ {
			lines = append(lines, fit(d[i], w))
		}
		for len(lines) < h-1 {
			lines = append(lines, "")
		}
		lines = append(lines, " "+key("↑↓ PgUp PgDn")+" scroll   "+key("G")+" end   "+key("esc/q")+" back")
		a.scr.draw(lines, -1, -1)
		k, ok := a.scr.keys.Read()
		if !ok {
			return
		}
		last := max(0, len(d)-body)
		switch {
		case k.Name == "up" || k.Rune == 'k':
			off = max(0, off-1)
		case k.Name == "down" || k.Rune == 'j':
			off = min(last, off+1)
		case k.Name == "pgup":
			off = max(0, off-body)
		case k.Name == "pgdn" || k.Rune == ' ':
			off = min(last, off+body)
		case k.Rune == 'g':
			off = 0
		case k.Rune == 'G':
			off = last
		case k.Rune == 'q' || k.Name == "esc" || k.Name == "ctrl-c" || k.Name == "enter" || k.Name == "left":
			return
		}
	}
}
