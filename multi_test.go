// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"os"
	"strings"
	"testing"
)

func parseAll(texts ...string) []*Crontab {
	var cts []*Crontab
	for _, t := range texts {
		cts = append(cts, ParseCrontab(t, false))
	}
	return cts
}

func rowCmds(rows []*mrow) []string {
	var out []string
	for _, r := range rows {
		var cs []string
		for _, l := range r.lines {
			cs = append(cs, l.Command)
		}
		out = append(out, strings.Join(cs, " | "))
	}
	return out
}

func TestMatchJobsByPath(t *testing.T) {
	cts := parseAll(
		"0 1 * * * /opt/backup.sh --full\n*/5 * * * * /usr/local/bin/poll\n0 3 * * * /only/here.sh\n",
		"30 2 * * * /opt/backup.sh --fast\n# */5 * * * * /usr/local/bin/poll\n",
	)
	rows, skipped := matchJobs(cts, []string{"/root", "/root"})
	got := rowCmds(rows)
	want := []string{
		"/opt/backup.sh --full | /opt/backup.sh --fast",
		"/usr/local/bin/poll | /usr/local/bin/poll",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if rows[0].exact || !rows[1].exact {
		t.Errorf("exact flags: %v %v", rows[0].exact, rows[1].exact)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped: %v", skipped)
	}
}

func TestMatchJobsInterpreterAndEnvPrefix(t *testing.T) {
	cts := parseAll(
		"0 1 * * * LANG=C bash /srv/job.sh a\n",
		"0 2 * * * /bin/bash /srv/job.sh b > /tmp/log 2>&1\n",
	)
	rows, _ := matchJobs(cts, []string{"/root", "/home/x"})
	if len(rows) != 1 || rows[0].path != "/srv/job.sh" {
		t.Fatalf("rows: %v", rowCmds(rows))
	}
}

func TestMatchJobsExactBeatsAmbiguousPath(t *testing.T) {
	// Two jobs run the same script; whole commands that match on every host
	// still pair up, and what is left over is reported, not guessed.
	cts := parseAll(
		"0 1 * * * /opt/rep.sh daily\n0 2 * * 0 /opt/rep.sh weekly\n0 4 * * * /opt/x.sh\n",
		"0 1 * * * /opt/rep.sh daily\n0 2 * * 0 /opt/rep.sh monthly\n0 4 * * * /opt/x.sh 1\n0 5 * * * /opt/x.sh 2\n",
	)
	rows, skipped := matchJobs(cts, []string{"/root", "/root"})
	got := rowCmds(rows)
	want := []string{
		"/opt/rep.sh daily | /opt/rep.sh daily",
		"/opt/rep.sh weekly | /opt/rep.sh monthly",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(skipped) != 1 || skipped[0].path != "/opt/x.sh" || skipped[0].counts[1] != 2 {
		t.Errorf("skipped: %+v", skipped)
	}
}

func TestMatchJobsThreeHosts(t *testing.T) {
	cts := parseAll(
		"0 1 * * * /a.sh\n0 2 * * * /b.sh\n",
		"0 1 * * * /a.sh\n0 2 * * * /b.sh\n",
		"0 1 * * * /a.sh x\n",
	)
	rows, _ := matchJobs(cts, []string{"/", "/", "/"})
	if len(rows) != 1 || rows[0].path != "/a.sh" {
		t.Fatalf("rows: %v", rowCmds(rows))
	}
}

func TestHostList(t *testing.T) {
	f := t.TempDir() + "/hosts"
	if err := os.WriteFile(f, []byte("# web\nweb1\nweb2  # second\n\nroot@db1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := hostList([]string{"a,b", "web1", ""}, f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "a b web1 web2 root@db1" {
		t.Errorf("got %v", got)
	}
}
