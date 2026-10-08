// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T, crontab string) *app {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	home := t.TempDir()
	a := &app{r: localRunner{}, info: remoteInfo{User: "tester", Home: home}}
	a.setContent(strings.ReplaceAll(crontab, "@HOME@", home))
	return a
}

func TestForegroundScript(t *testing.T) {
	a := testApp(t, "FOO=bar baz\n* * * * * echo \"$FOO|$PWD|$LOGNAME|${CRONMAN_LEAK:-clean}\"; cat; echo err >&2; exit 3%in \\%1%in 2\n")
	t.Setenv("CRONMAN_LEAK", "leaked")
	out, _, err := localRunner{}.Exec(a.foregroundScript(a.jobs[0]), "")
	ee, ok := err.(interface{ ExitCode() int })
	if !ok || ee.ExitCode() != 3 {
		t.Fatalf("want exit 3, got %v (out %q)", err, out)
	}
	want := "bar baz|" + a.info.Home + "|tester|clean\nin %1\nin 2\nerr\n"
	if got := strings.ReplaceAll(out, "/private", ""); got != strings.ReplaceAll(want, "/private", "") {
		t.Fatalf("got %q\nwant %q", out, want)
	}
}

func TestBackgroundScript(t *testing.T) {
	a := testApp(t, "HOME=@HOME@\n* * * * * echo started; cat%hello\n")
	t.Setenv("HOME", a.info.Home)
	out, stderr, err := localRunner{}.Exec(a.backgroundScript(a.jobs[0], 1), "")
	if err != nil || !strings.Contains(out, "@@PID ") {
		t.Fatalf("start failed: %v %q %q", err, out, stderr)
	}
	_, log, _ := strings.Cut(out, "@@LOG ")
	log = strings.TrimSpace(log)
	if filepath.Dir(log) != filepath.Join(a.info.Home, ".cronman_runs") {
		t.Fatalf("log in wrong place: %s", log)
	}
	for i := 0; i < 50; i++ {
		if b, _ := os.ReadFile(log); string(b) == "started\nhello\n" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(log)
	t.Fatalf("log = %q", b)
}
