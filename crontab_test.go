package main

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyCommentedLines(t *testing.T) {
	cases := []struct {
		line     string
		kind     LineKind
		disabled bool
	}{
		// commented-out jobs, any number of '#' and spacing
		{"#0 5 * * * /opt/backup.sh", KJob, true},
		{"# 0 5 * * * /opt/backup.sh", KJob, true},
		{"## 0 5 * * * /opt/backup.sh", KJob, true},
		{"###   */10 * * * * /usr/bin/test -x /x && /x", KJob, true},
		{"  #\t@daily /usr/local/bin/rotate", KJob, true},
		{"#@reboot /home/u/start.sh", KJob, true},
		{"## 30 2 * * mon-fri test -f /tmp/flag && rm /tmp/flag", KJob, true},
		// prose and headers stay comments
		{"# m h  dom mon dow   command", KComment, false},
		{"## backup runs at 5 every day", KComment, false},
		{"# test", KComment, false},
		{"## test 1 2 3", KComment, false},
		{"# 5 is the number of retries", KComment, false},
		{"#MAILTO=root", KComment, false},
		{"#", KComment, false},
		{"##########", KComment, false},
		{"# 61 * * * * /bad/minute", KComment, false},
		{"# */0 * * * * /bad/step", KComment, false},
		{"# 0 5 * * *", KComment, false}, // schedule without command
		{"# 0 5 * * * command to be executed", KComment, false},
		// active lines
		{"0 5 * * * /opt/backup.sh", KJob, false},
		{"@hourly test -x /usr/bin/foo && /usr/bin/foo", KJob, false},
		{"MAILTO=\"\"", KEnv, false},
		{"PATH=/usr/local/bin:/usr/bin:/bin", KEnv, false},
		{"", KBlank, false},
		{"   ", KBlank, false},
		{"61 * * * * /x", KInvalid, false},
		{"hello world", KInvalid, false},
	}
	for _, c := range cases {
		l := parseLine(c.line, false)
		if l.Kind != c.kind || l.Disabled != c.disabled {
			t.Errorf("%q: got kind=%d disabled=%v, want kind=%d disabled=%v (err=%s)", c.line, l.Kind, l.Disabled, c.kind, c.disabled, l.Err)
		}
	}
}

func TestDebianHeaderExamplesAreComments(t *testing.T) {
	header := `# Edit this file to introduce tasks to be run by cron.
#
# For example, you can run a backup of all your user accounts
# at 5 a.m every week with:
# 0 5 * * 1 tar -zcf /var/backups/home.tgz /home/
#
# m h  dom mon dow   command
0 3 * * * /real/job.sh
# 0 4 * * * /disabled/job.sh
`
	c := ParseCrontab(header, false)
	jobs := c.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Command != "/real/job.sh" || jobs[0].Disabled {
		t.Errorf("first job wrong: %+v", jobs[0])
	}
	if jobs[1].Command != "/disabled/job.sh" || !jobs[1].Disabled {
		t.Errorf("second job wrong: %+v", jobs[1])
	}
}

func TestSystemCrontabHeader(t *testing.T) {
	etc := `SHELL=/bin/sh
# Example of job definition:
# .---------------- minute (0 - 59)
# |  .------------- hour (0 - 23)
# *  *  *  *  * user-name command to be executed
17 *	* * *	root	cd / && run-parts --report /etc/cron.hourly
#25 6	* * *	root	test -x /usr/sbin/anacron || run-parts --report /etc/cron.daily
`
	c := ParseCrontab(etc, true)
	jobs := c.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d: %+v", len(jobs), jobs)
	}
	if jobs[0].User != "root" || jobs[0].Disabled {
		t.Errorf("job0: %+v", jobs[0])
	}
	if !jobs[1].Disabled || jobs[1].User != "root" {
		t.Errorf("job1: %+v", jobs[1])
	}
}

func TestToggleRoundTrip(t *testing.T) {
	src := "## 0 5 * * * /a.sh\n*/5 * * * *\t/b.sh  >> /tmp/b.log 2>&1\n"
	c := ParseCrontab(src, false)
	j := c.Jobs()
	j[0].SetDisabled(false)
	j[1].SetDisabled(true)
	want := "0 5 * * * /a.sh\n# */5 * * * *\t/b.sh  >> /tmp/b.log 2>&1\n"
	if got := c.Render(); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	j[0].SetDisabled(true)
	j[1].SetDisabled(false)
	if got := c.Render(); got != src {
		t.Errorf("round trip: got %q want %q", got, src)
	}
}

func TestEditPreservesOthers(t *testing.T) {
	src := "MAILTO=me\n# note\n0 5 * * * /a.sh\n# 1 1 * * * /b.sh\n"
	c := ParseCrontab(src, false)
	j := c.Jobs()
	if err := j[0].SetSchedule("*/15  9-17 * * 1-5"); err != nil {
		t.Fatal(err)
	}
	if err := j[1].SetCommand("/c.sh --x"); err != nil {
		t.Fatal(err)
	}
	want := "MAILTO=me\n# note\n*/15 9-17 * * 1-5 /a.sh\n# 1 1 * * * /c.sh --x\n"
	if got := c.Render(); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if err := j[0].SetSchedule("* * *"); err == nil {
		t.Error("expected error for 3-field schedule")
	}
}

func TestValidateSchedule(t *testing.T) {
	good := []string{"* * * * *", "*/5 * * * *", "0 0 1 1 *", "0-59/15 0,12 1-31 jan-dec mon-fri", "0 0 * * 7", "@daily", "@REBOOT", "5/10 * * * *"}
	bad := []string{"60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "5-1 * * * *", "@sometimes", "* * * * * *", "a * * * *", ",1 * * * *"}
	for _, s := range good {
		if err := ValidateSchedule(s); err != nil {
			t.Errorf("%q should be valid: %v", s, err)
		}
	}
	for _, s := range bad {
		if err := ValidateSchedule(s); err == nil {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestNextRuns(t *testing.T) {
	from := time.Date(2026, 10, 8, 12, 7, 30, 0, time.UTC) // Thursday
	cases := []struct {
		sched string
		want  []string
	}{
		{"*/5 * * * *", []string{"12:10", "12:15"}},
		{"0 3 * * *", []string{"10-09 03:00", "10-10 03:00"}},
		{"30 8 * * 1-5", []string{"10-09 08:30", "10-12 08:30"}},
		{"0 0 13 * 5", []string{"10-09 00:00", "10-13 00:00"}}, // dom OR dow
		{"@monthly", []string{"11-01 00:00", "12-01 00:00"}},
	}
	for _, c := range cases {
		ts, err := NextRuns(c.sched, from, len(c.want))
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range c.want {
			f := "01-02 15:04"
			if len(w) == 5 {
				f = "15:04"
			}
			if i >= len(ts) || ts[i].Format(f) != w {
				t.Errorf("%s: run %d got %v want %s", c.sched, i, ts, w)
				break
			}
		}
	}
}

func TestDescribe(t *testing.T) {
	cases := map[string]string{
		"*/5 * * * *":  "every 5 minutes",
		"7 * * * *":    "hourly at :07",
		"0 3 * * *":    "daily at 03:00",
		"30 8 * * 1-5": "on Mon-Fri at 08:30",
		"@reboot":      "at boot",
	}
	for s, want := range cases {
		if got := Describe(s); got != want {
			t.Errorf("%s: got %q want %q", s, got, want)
		}
	}
}

func TestCronShellPart(t *testing.T) {
	if got := CronShellPart(`date +\%Y-\%m >> /tmp/x%stdin`); got != "date +%Y-%m >> /tmp/x" {
		t.Errorf("got %q", got)
	}
	if !strings.Contains(CronShellPart("/a.sh"), "/a.sh") {
		t.Error("plain")
	}
}
