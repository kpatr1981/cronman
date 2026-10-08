package main

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoteChecksLocally(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("check script needs a POSIX shell")
	}
	if os.Getuid() == 0 {
		t.Skip("permission tests are meaningless as root")
	}
	d := t.TempDir()
	d, _ = filepath.EvalSymlinks(d)
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(d, name)
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.sh", "#!/bin/sh\necho ok\n", 0o755)
	noexec := write("noexec.sh", "#!/bin/sh\necho ok\n", 0o644)
	crlf := write("crlf.sh", "#!/bin/sh\r\necho ok\r\n", 0o755)
	badInterp := write("badinterp.sh", "#!/usr/bin/nonexistent-shell\necho\n", 0o755)
	envNode := write("envnode.js", "#!/usr/bin/env cronman-nonexistent-tool\n", 0o755)
	noShebang := write("noshebang", "echo hi\n", 0o755)
	unreadable := write("unreadable.sh", "#!/bin/sh\n", 0o311) // --x--x--x
	locked := filepath.Join(d, "locked")
	os.Mkdir(locked, 0o755)
	inLocked := filepath.Join(locked, "x.sh")
	os.WriteFile(inLocked, []byte("#!/bin/sh\n"), 0o755)
	os.Chmod(locked, 0o600)
	defer os.Chmod(locked, 0o755)
	link := filepath.Join(d, "broken")
	os.Symlink(filepath.Join(d, "nowhere.sh"), link)
	ro := write("ro.log", "", 0o444)

	env := map[string]string{"HOME": d, "PATH": "/usr/bin:/bin"}
	type tc struct {
		cmd     string
		sev     Severity
		contain string
	}
	cases := []tc{
		{good, SevOK, ""},
		{noexec, SevFail, "not executable"},
		{crlf, SevFail, "ends with CR"},
		{badInterp, SevFail, "interpreter /usr/bin/nonexistent-shell"},
		{envNode, SevFail, "not in cron's PATH"},
		{noShebang, SevWarn, "no #! line"},
		{unreadable, SevFail, "not readable"},
		{inLocked, SevFail, "cannot be entered"},
		{link, SevFail, "broken symlink"},
		{filepath.Join(d, "missing.sh"), SevFail, "does not exist"},
		{"cronman-no-such-cmd --x", SevFail, "not found in cron's PATH"},
		{"ls -l", SevOK, ""},
		{good + " >> " + ro, SevFail, "not writable"},
		{good + " >> " + filepath.Join(d, "new.log"), SevOK, ""},
		{good + " >> " + filepath.Join(d, "nodir", "x.log"), SevFail, "does not exist, so"},
		{"/bin/sh " + noexec, SevOK, ""},   // run via interpreter: needs read only
		{"/bin/sh " + crlf, SevWarn, "CR"}, // CRLF script fed to sh
		{"cd " + locked + " && ./x.sh", SevFail, "cannot cd"},
	}
	u, _ := user.Current()
	ctx := UserCtx{User: u.Username, BSD: runtime.GOOS == "darwin"}

	var reqs []CheckReq
	exs := make([]*Extraction, len(cases))
	for i, c := range cases {
		exs[i] = ExtractTargets(c.cmd, env)
		for _, tg := range exs[i].Targets {
			reqs = append(reqs, reqFor(tg))
		}
	}
	res, err := RunChecks(localRunner{}, reqs)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		jr := &JobReport{}
		var msgs []string
		for _, tg := range exs[i].Targets {
			tr := Analyze(tg, res[reqFor(tg).key()], ctx)
			jr.Targets = append(jr.Targets, tr)
			for _, is := range tr.Issues {
				msgs = append(msgs, is.Msg)
			}
		}
		all := strings.Join(msgs, " || ")
		if jr.Sev() != c.sev || (c.contain != "" && !strings.Contains(all, c.contain)) {
			t.Errorf("%s\n  sev=%d want %d\n  msgs: %s", c.cmd, jr.Sev(), c.sev, all)
		}
	}

	// Fixes for files we own must not need sudo, and must actually work.
	tr := Analyze(Target{Kind: TExec, Role: "command", Path: noexec}, res[CheckReq{TExec, noexec, "/usr/bin:/bin"}.key()], ctx)
	if tr.Res == nil {
		t.Fatal("no result for noexec")
	}
	f := tr.Issues[0].Fixes[0]
	if f.Sudo || f.Cmd != "chmod u+x "+shq(noexec) {
		t.Errorf("fix for owned file: %+v", f)
	}
	if _, _, err := (localRunner{}).Exec(f.Cmd, ""); err != nil {
		t.Fatal(err)
	}
	crTr := Analyze(Target{Kind: TExec, Role: "command", Path: crlf}, res[CheckReq{TExec, crlf, "/usr/bin:/bin"}.key()], ctx)
	if _, se, err := (localRunner{}).Exec(crTr.Issues[0].Fixes[0].Cmd, ""); err != nil {
		t.Fatalf("crlf fix failed: %v %s", err, se)
	}
	b, _ := os.ReadFile(crlf)
	if strings.Contains(string(b), "\r") {
		t.Error("CRLF fix did not strip CR")
	}
	res2, _ := RunChecks(localRunner{}, []CheckReq{{TExec, noexec, ""}, {TExec, crlf, ""}})
	for _, p := range []string{noexec, crlf} {
		tr := Analyze(Target{Kind: TExec, Role: "command", Path: p}, res2[CheckReq{TExec, p, ""}.key()], ctx)
		if tr.Sev() != SevOK {
			t.Errorf("%s still failing after fix: %+v", p, tr.Issues)
		}
	}
}

func TestPermFixesForOtherOwner(t *testing.T) {
	ctx := UserCtx{User: "alice", Groups: []string{"alice", "devs"}}
	fs := permFixes("/opt/x.sh", ownerInfo{"root", "devs", "750"}, "x", ctx, false)
	if fs[0].Cmd != "chmod g+x /opt/x.sh" || !fs[0].Sudo {
		t.Errorf("%+v", fs)
	}
	fs = permFixes("/opt/x.sh", ownerInfo{"root", "root", "644"}, "rx", ctx, false)
	if fs[0].Cmd != "chmod o+rx /opt/x.sh" || fs[1].Cmd != "chown alice /opt/x.sh && chmod u+rx /opt/x.sh" {
		t.Errorf("%+v", fs)
	}
}
