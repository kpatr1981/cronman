// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var histNow = time.Date(2026, 10, 13, 16, 20, 0, 0, time.FixedZone("EEST", 3*3600))

func TestWrapStatusRoundTrip(t *testing.T) {
	for _, cmd := range []string{
		"/home/a/bin/good.sh >> /home/a/logs/good.log 2>&1",
		"cd /srv && ./run.sh;",
		`tar czf /tmp/bk-$(date +\%F).tgz /etc`,
		"mail -s hi root%line one%line two",
	} {
		w, err := WrapStatus(cmd, "a1b2")
		if err != nil {
			t.Fatalf("%q: %v", cmd, err)
		}
		inner, id, ok := UnwrapStatus(w)
		want := strings.TrimSuffix(cmd, ";")
		if i := cronPercent(cmd); i >= 0 {
			want = strings.TrimSpace(cmd[:i]) + cmd[i:]
		}
		if !ok || id != "a1b2" || inner != want {
			t.Errorf("%q: unwrap gave %q %q %v", cmd, inner, id, ok)
		}
		if again, _ := WrapStatus(w, "ffff"); again != w {
			t.Errorf("%q: wrapping twice changed it", cmd)
		}
		if s, has := CronStdin(cmd); has {
			if s2, _ := CronStdin(w); s2 != s {
				t.Errorf("%q: stdin changed: %q → %q", cmd, s, s2)
			}
		}
	}
	for _, bad := range []string{"sleep 100 &", "run.sh # nightly", "  ", "x.sh"} {
		id := "a1"
		if bad == "x.sh" {
			id = "XY" // not hex
		}
		if _, err := WrapStatus(bad, id); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

// The wrapper must record the exit status and keep it as the job's status.
func TestWrapStatusRuns(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, statusDir), 0o700); err != nil {
		t.Fatal(err)
	}
	w, _ := WrapStatus("echo hi; (exit 3)", "c0de")
	cmd := exec.Command("/bin/sh", "-c", CronShellPart(w))
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 3 || string(out) != "hi\n" {
		t.Fatalf("exit %v, output %q", err, out)
	}
	data, err := os.ReadFile(filepath.Join(home, statusDir, "c0de"))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(data))
	if len(f) != 3 || f[2] != "3" {
		t.Fatalf("status file: %q", data)
	}
}

func TestParseLogLine(t *testing.T) {
	cases := []struct {
		line, kind, user, cmd, pid string
		exit                       int
	}{
		{"2026-10-13T16:13:01+03:00 lima-cm-debian CRON[1612]: (alice) CMD ([1617] /bin/true ok-job)", "CMD", "alice", "/bin/true ok-job", "1612", 0},
		{"2026-10-13T16:12:00+03:00 lima-cm-debian CRON[1511]: (alice) CMD (/bin/true ok-job)", "CMD", "alice", "/bin/true ok-job", "1511", 0},
		{"2026-10-13T16:13:01+03:00 lima-cm-debian CRON[1611]: (CRON) error (grandchild #1619 failed with exit status 1)", "FAIL", "", "", "1611", 1},
		{"2026-10-13T16:13:01+03:00 lima-cm-debian CRON[1610]: (alice) END ([1615] echo hello-out)", "END", "alice", "echo hello-out", "1610", 0},
		{"Oct 13 16:12:01 lima-cm-rocky CROND[2843]: (alice) CMDEND (/bin/true ok-job)", "CMDEND", "alice", "/bin/true ok-job", "2843", 0},
		{"Oct 13 16:12:01 lima-cm-rocky CROND[2841]: (alice) CMDOUT (hello-out)", "CMDOUT", "alice", "hello-out", "2841", 0},
		{"Oct 13 16:12:00 lima-cm-alpine cron.info crond[2385]: USER alice pid 2522 cmd /bin/true ok-job", "CMD", "alice", "/bin/true ok-job", "2385", 0},
		{"2026-10-13T16:12:00.123456+03:00 host CRON[9]: (bob) CMD (x)", "CMD", "bob", "x", "9", 0},
		{"2026-10-13T16:12:00+0300 lima-cm-rocky CROND[1]: (root) CMD (run-parts /etc/cron.hourly)", "CMD", "root", "run-parts /etc/cron.hourly", "1", 0},
	}
	for _, c := range cases {
		ev, ok := parseLogLine(c.line, histNow)
		if !ok || ev.kind != c.kind || ev.user != c.user || ev.cmd != c.cmd || ev.pid != c.pid || ev.exit != c.exit {
			t.Errorf("%s\n  got %+v %v", c.line, ev, ok)
		}
		if ok && (ev.t.Month() != 10 || ev.t.Day() != 13 || ev.t.Year() != 2026) {
			t.Errorf("%s: time %v", c.line, ev.t)
		}
	}
	for _, bad := range []string{
		"Oct 13 16:11:01 lima-cm-rocky crond[790]: (CRON) STARTUP (1.5.7)",
		"2026-10-13T16:12:00+03:00 h CRON[1503]: pam_unix(cron:session): session opened for user alice",
		"garbage",
	} {
		if ev, ok := parseLogLine(bad, histNow); ok {
			t.Errorf("%q parsed as %+v", bad, ev)
		}
	}
	// A traditional timestamp from December read in January is last year.
	ev, _ := parseLogLine("Dec 31 23:59:00 h CROND[1]: (a) CMD (x)", time.Date(2027, 1, 1, 0, 5, 0, 0, time.UTC))
	if ev.t.Year() != 2026 {
		t.Errorf("year rollover: %v", ev.t)
	}
}

func jobsOf(t *testing.T, crontab string) []*Line {
	t.Helper()
	return ParseCrontab(crontab, false).Jobs()
}

func TestHistoriesDebianLevel15(t *testing.T) {
	// The 16:12 run was logged before cron was restarted with -L 15.
	out := `@@SRC journal
2026-10-13T16:12:00+03:00 h CRON[1511]: (alice) CMD (/bin/true ok-job)
2026-10-13T16:13:01+03:00 h CRON[1612]: (alice) CMD ([1617] /bin/true ok-job)
2026-10-13T16:13:01+03:00 h CRON[1611]: (alice) CMD ([1619] /bin/false fail-job)
2026-10-13T16:13:01+03:00 h CRON[1612]: (alice) END ([1617] /bin/true ok-job)
2026-10-13T16:13:01+03:00 h CRON[1611]: (CRON) error (grandchild #1619 failed with exit status 1)
2026-10-13T16:13:01+03:00 h CRON[1611]: (alice) END ([1619] /bin/false fail-job)
2026-10-13T16:13:01+03:00 h CRON[1700]: (bob) CMD ([1701] /bin/true ok-job)
@@USER alice
@@PS
  571 root     /usr/sbin/cron -f -P -L 15
`
	h := parseHostRuns(out, histNow)
	if h.logLevel != 15 || !h.logOK() {
		t.Fatalf("level %d ok %v", h.logLevel, h.logOK())
	}
	jobs := jobsOf(t, "* * * * * /bin/true ok-job\n* * * * * /bin/false fail-job\n")
	hs := h.Histories("alice", jobs)
	if runs := hs[jobs[0]].Runs; len(runs) != 2 || runs[0].Result != RunUnknown || runs[1].Result != RunOK {
		t.Errorf("ok-job: %+v", runs)
	}
	if r := hs[jobs[1]].Last(); r == nil || r.Result != RunFailed || r.Exit != 1 {
		t.Errorf("fail-job: %+v", r)
	}
}

func TestHistoriesCronieAndBusybox(t *testing.T) {
	cronie := `@@SRC /var/log/cron
Oct 13 16:12:01 h CROND[2857]: (alice) CMD (echo hello-out)
Oct 13 16:12:01 h CROND[2841]: (alice) CMDOUT (hello-out)
Oct 13 16:12:01 h CROND[2841]: (alice) CMDEND (echo hello-out)
Oct 13 16:13:01 h CROND[2900]: (alice) CMD (echo hello-out)
@@PS
  790 root     /usr/sbin/crond -n
 2950 alice    /bin/sh -c echo hello-out
`
	jobs := jobsOf(t, "* * * * * echo hello-out\n")
	jh := parseHostRuns(cronie, histNow).Histories("alice", jobs)[jobs[0]]
	if len(jh.Runs) != 2 || jh.Runs[0].Result != RunUnknown || jh.Runs[0].End.IsZero() || strings.Join(jh.Runs[0].Output, "|") != "hello-out" {
		t.Errorf("cronie runs: %+v", jh.Runs[0])
	}
	if len(jh.Running) != 1 || jh.Running[0] != "2950" {
		t.Errorf("running: %v", jh.Running)
	}

	bb := `@@SRC /var/log/messages
Oct 13 16:12:00 h cron.info crond[2385]: USER alice pid 2522 cmd /bin/true ok-job
Oct 13 16:12:00 h cron.info crond[2385]: USER root pid 2523 cmd /bin/true ok-job
`
	jobs = jobsOf(t, "* * * * * /bin/true ok-job\n")
	jh = parseHostRuns(bb, histNow).Histories("alice", jobs)[jobs[0]]
	if len(jh.Runs) != 1 || jh.Runs[0].Result != RunUnknown {
		t.Errorf("busybox runs: %+v", jh.Runs)
	}
}

// Two jobs with the same command: the run goes to the one whose schedule matches.
func TestHistoriesSameCommand(t *testing.T) {
	out := "@@SRC journal\n2026-10-13T03:00:00+03:00 h CRON[5]: (a) CMD (/x.sh)\n"
	jobs := jobsOf(t, "0 2 * * * /x.sh\n0 3 * * * /x.sh\n")
	hs := parseHostRuns(out, histNow).Histories("a", jobs)
	if len(hs[jobs[0]].Runs) != 0 || len(hs[jobs[1]].Runs) != 1 {
		t.Errorf("runs went to the wrong job: %d %d", len(hs[jobs[0]].Runs), len(hs[jobs[1]].Runs))
	}
}

func TestHistoriesStatusFiles(t *testing.T) {
	w, _ := WrapStatus("/opt/job.sh", "beef")
	start := histNow.Add(-2 * time.Hour)
	out := "@@NOREAD journal\n@@USER a\n@@STATUS beef\n" +
		itoa(start.Unix()) + " " + itoa(start.Add(5*time.Second).Unix()) + " 0\n" +
		itoa(start.Add(time.Hour).Unix()) + " " + itoa(start.Add(time.Hour+9*time.Second).Unix()) + " 2\n@@PS\n"
	jobs := jobsOf(t, "0 * * * * "+w+"\n")
	h := parseHostRuns(out, histNow)
	if h.logOK() || len(h.noRead) != 1 {
		t.Fatalf("log state: %v %v", h.logOK(), h.noRead)
	}
	jh := h.Histories("a", jobs)[jobs[0]]
	last := jh.Last()
	if !jh.Wrapped || len(jh.Runs) != 2 || last.Result != RunFailed || last.Exit != 2 || last.End.Sub(last.Start) != 9*time.Second {
		t.Fatalf("runs: %+v", jh.Runs)
	}
	if ok := jh.LastWith(RunOK); ok == nil || !ok.Start.Equal(start) {
		t.Errorf("last success: %+v", ok)
	}
	if jh.Missed != nil {
		t.Errorf("missed without a readable log: %v", jh.Missed)
	}
	if c := jh.lastCell(histNow, false); !strings.Contains(c, "rc=2") {
		t.Errorf("cell %q", c)
	}
}

func TestMissedRuns(t *testing.T) {
	at := func(h, m int) *Run { return &Run{Start: time.Date(2026, 10, 13, h, m, 1, 0, histNow.Location())} }
	// Hourly at :00; the 14:00 and 16:00 runs are missing; 16:20 is now.
	runs := []*Run{at(12, 0), at(13, 0), at(15, 0)}
	m := missedRuns("0 * * * *", runs, histNow)
	if len(m) != 2 || m[0].Hour() != 14 || m[1].Hour() != 16 {
		t.Fatalf("missed %v", m)
	}
	jh := &JobHistory{Runs: runs, Missed: m, LogOK: true}
	if !jh.MissedLast() || jh.Sev() != SevWarn {
		t.Error("16:00 should count as missed")
	}
	// The run for the current minute is not judged yet.
	if m := missedRuns("* * * * *", []*Run{at(16, 18), at(16, 19)}, histNow); len(m) != 0 {
		t.Errorf("every minute: %v", m)
	}
	if m := missedRuns("0 3 * * *", nil, histNow); m != nil {
		t.Errorf("never seen: %v", m)
	}
}

func TestWrappedJobKeepsPath(t *testing.T) {
	w, _ := WrapStatus("/opt/backup.sh --full >> /var/log/b.log 2>&1", "ab")
	ct := ParseCrontab("0 1 * * * "+w+"\n", false)
	if p := jobPath(ct, ct.Jobs()[0], "/home/a"); p != "/opt/backup.sh" {
		t.Errorf("path %q", p)
	}
	l := ct.Jobs()[0]
	ct2 := ParseCrontab("0 1 * * * /opt/backup.sh --full >> /var/log/b.log 2>&1\n", false)
	rows, _ := matchJobs([]*Crontab{ct, ct2}, []string{"/home/a", "/home/a"})
	if len(rows) != 1 || !rows[0].exact || rows[0].lines[0] != l {
		t.Errorf("wrapped and plain job should pair exactly: %+v", rows)
	}
}

func TestLastMail(t *testing.T) {
	mbox := "From root@h  Mon Oct 13 01:00:01 2026\nSubject: Cron <a@h> /opt/other.sh\nDate: x\n\nother\n" +
		"From root@h  Mon Oct 13 02:00:01 2026\nSubject: Cron <a@h> /opt/backup.sh --full\nDate: Mon, 13 Oct 2026 02:00:01\n\nline1\nline2\n" +
		"From root@h  Mon Oct 13 03:00:01 2026\nSubject: hello\n\nnot cron\n"
	date, body := lastMail(mbox, "/opt/backup.sh --full")
	if date != "Mon, 13 Oct 2026 02:00:01" || len(body) < 2 || body[0] != "line1" {
		t.Errorf("got %q %q", date, body)
	}
	if _, body := lastMail(mbox, "/nope"); body != nil {
		t.Errorf("unexpected mail %q", body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
