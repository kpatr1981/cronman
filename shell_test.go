// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"fmt"
	"strings"
	"testing"
)

func targetsStr(ex *Extraction) string {
	var parts []string
	for _, t := range ex.Targets {
		parts = append(parts, fmt.Sprintf("%c:%s:%s", t.Kind, t.Role, t.Path))
	}
	return strings.Join(parts, " | ")
}

func TestExtractTargets(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "PATH": "/usr/bin:/bin", "LOGDIR": "/var/log/app"}
	cases := []struct{ cmd, want string }{
		{"/opt/x/backup.sh", "x:command:/opt/x/backup.sh"},
		{"~/bin/run.sh >> ~/logs/run.log 2>&1", "w:output:/home/u/logs/run.log | x:command:/home/u/bin/run.sh"},
		{"./script.sh", "x:command:/home/u/script.sh"},
		{"cd /srv/app && ./manage.sh --daily > /dev/null 2>&1", "d:cd directory:/srv/app | x:command:/srv/app/manage.sh"},
		{"/usr/bin/python3 /opt/r/report.py --x 1", "x:interpreter:/usr/bin/python3 | r:script:/opt/r/report.py"},
		{"python3 -u scripts/job.py", "b:interpreter:python3 | r:script:/home/u/scripts/job.py"},
		{"python3 -m mypkg.task", "b:interpreter:python3"},
		{"bash -c 'cd /a && ./b.sh'", "b:interpreter:bash | d:cd directory:/a | x:command:/a/b.sh"},
		{"nice -n 10 ionice -c3 /usr/local/bin/heavy --all", "b:wrapper:nice | b:wrapper:ionice | x:command:/usr/local/bin/heavy"},
		{"timeout 30m /opt/sync.sh", "b:wrapper:timeout | x:command:/opt/sync.sh"},
		{"flock -n /tmp/sync.lock /opt/sync.sh", "b:wrapper:flock | w:lock file:/tmp/sync.lock | x:command:/opt/sync.sh"},
		{"/usr/bin/flock -n /var/lock/x -c '/opt/x.sh arg'", "x:wrapper:/usr/bin/flock | w:lock file:/var/lock/x | x:command:/opt/x.sh"},
		{"test -x /usr/sbin/anacron || run-parts --report /etc/cron.daily", "b:command:run-parts | d:run-parts directory:/etc/cron.daily"},
		{"node app.js", "b:interpreter:node | r:script:/home/u/app.js"},
		{"$HOME/bin/a.sh", "x:command:/home/u/bin/a.sh"},
		{"${LOGDIR}/rotate", "x:command:/var/log/app/rotate"},
		{"$UNKNOWN/a.sh", ""},
		{"/bin/echo hi > \"/tmp/my file.log\"", "w:output:/tmp/my file.log | x:command:/bin/echo"},
		{"echo hi | mail -s subj root", "b:command:mail"},
		{"PATH=/opt/bin:$PATH mytool", "b:command:mytool"},
		{"env FOO=1 /opt/t.sh", "b:wrapper:env | x:command:/opt/t.sh"},
		{"sudo /opt/root.sh", "b:command:sudo"},
		{"/opt/a.sh; /opt/b.sh", "x:command:/opt/a.sh | x:command:/opt/b.sh"},
		{"java -jar /opt/app.jar", "b:interpreter:java | r:script:/opt/app.jar"},
		{"date +\\%F > /tmp/d%ignored", "w:output:/tmp/d | b:command:date"},
		{"/opt/x.sh < /etc/input.txt", "r:input:/etc/input.txt | x:command:/opt/x.sh"},
		{". /etc/profile; /opt/x.sh", "r:sourced file:/etc/profile | x:command:/opt/x.sh"},
	}
	for _, c := range cases {
		got := targetsStr(ExtractTargets(c.cmd, env))
		if got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.cmd, got, c.want)
		}
	}
}

func TestBareTargetUsesCrontabPath(t *testing.T) {
	ex := ExtractTargets("PATH=/x:/y foo", map[string]string{"HOME": "/h", "PATH": "/usr/bin:/bin"})
	if len(ex.Targets) != 1 || ex.Targets[0].PathVar != "/x:/y" {
		t.Errorf("%+v", ex.Targets)
	}
}

func TestUnresolvedNotes(t *testing.T) {
	ex := ExtractTargets("$(which foo) --x", map[string]string{"HOME": "/h", "PATH": "/bin"})
	if len(ex.Targets) != 0 || len(ex.Notes) == 0 {
		t.Errorf("targets=%v notes=%v", ex.Targets, ex.Notes)
	}
}

func TestMainTargetAndApplyEdit(t *testing.T) {
	env := map[string]string{"HOME": "/home/u", "PATH": "/usr/bin:/bin"}
	cmd := "cd /srv && ~/bin/run.sh >> ~/run.log 2>&1"
	mt := MainTarget(ExtractTargets(cmd, env).Targets)
	if mt == nil || mt.Word != "~/bin/run.sh" {
		t.Fatalf("main target %+v", mt)
	}
	got, ok := ApplyEdit(cmd, mt.Word, "/opt/run2.sh")
	if !ok || got != "cd /srv && /opt/run2.sh >> ~/run.log 2>&1" {
		t.Errorf("got %q", got)
	}
	// must not replace partial matches
	if _, ok := ApplyEdit("/opt/run.sh.bak", "/opt/run.sh", "/x"); ok {
		t.Error("partial match replaced")
	}
	mt = MainTarget(ExtractTargets("/usr/bin/python3 /opt/r.py", env).Targets)
	if mt.Path != "/opt/r.py" {
		t.Errorf("python main target %+v", mt)
	}
}
