package main

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type auditOpts struct {
	verbose bool   // show OK jobs and disabled jobs too
	fix     bool   // offer to apply fixes interactively
	root    string // hidden: path prefix for system files (testing / mounted images)
}

type auditFile struct {
	kind   string // user | system | crond
	path   string
	st     ownerInfo
	user   string // owner user for user crontabs
	ct     *Crontab
	issues []Issue
	jobs   []*auditJob
}

type auditJob struct {
	file    *auditFile
	line    *Line
	runUser string
	ext     *Extraction
	rep     *JobReport
	issues  []Issue // job-level (unknown user, invalid line…)
}

type auditPart struct {
	period string
	path   string
	st     ownerInfo
	exec   bool
	dir    bool
	issues []Issue
	rep    *TargetReport
}

type auditState struct {
	r        Runner
	opts     auditOpts
	prefix   string // privilege prefix for remote commands
	stdinPre string // sudo password line
	asUser   string
	isRoot   bool
	os       string
	debian   bool
	daemon   string
	files    []*auditFile
	parts    []*auditPart
	passwd   map[string]string // user -> home
	lists    map[string][]string
	listSeen map[string]bool
	unread   []string
	groups   map[string][]string
	noSwitch bool
}

func (s *auditState) run(script string) (string, string, error) {
	return s.r.Exec(strings.TrimSpace(s.prefix+" /bin/sh -s"), s.stdinPre+script)
}

func (s *auditState) dumpScript() string {
	R := shq(s.opts.root)
	if s.opts.root == "" {
		R = "''"
	}
	return `R=` + R + `
printf '@@ID %s %s\n' "$(id -un)" "$(id -u)"
printf '@@OS %s\n' "$(uname -s)"
[ -f "$R/etc/debian_version" ] && echo "@@DEBIAN"
if [ "$(uname -s)" != Darwin ] && command -v pgrep >/dev/null 2>&1; then
  if pgrep -x cron >/dev/null 2>&1 || pgrep -x crond >/dev/null 2>&1 || pgrep -x cronie >/dev/null 2>&1; then echo "@@DAEMON running"; else echo "@@DAEMON not-running"; fi
fi
st() { stat -c '%U:%G:%a' "$1" 2>/dev/null || stat -f '%Su:%Sg:%Lp' "$1" 2>/dev/null; }
dumpf() { printf '@@FILE\t%s\t%s\t%s\n' "$1" "$(st "$2")" "$2"; cat "$2"; printf '\n@@ENDFILE\n'; }
for d in "$R/var/spool/cron/crontabs" "$R/var/spool/cron/tabs" "$R/var/spool/cron" "$R/var/cron/tabs" "$R/usr/lib/cron/tabs"; do
  [ -d "$d" ] || continue
  if [ ! -r "$d" ] || [ ! -x "$d" ]; then printf '@@UNREADABLE\t%s\n' "$d"; continue; fi
  for f in "$d"/*; do
    [ -f "$f" ] || continue
    case ${f##*/} in .*|tmp.*|*.bak|*~) continue;; esac
    if [ -r "$f" ]; then dumpf user "$f"; else printf '@@UNREADABLE\t%s\n' "$f"; fi
  done
done
if [ -f "$R/etc/crontab" ]; then
  if [ -r "$R/etc/crontab" ]; then dumpf system "$R/etc/crontab"; else printf '@@UNREADABLE\t%s\n' "$R/etc/crontab"; fi
fi
if [ -d "$R/etc/cron.d" ]; then
  for f in "$R/etc/cron.d"/*; do
    [ -f "$f" ] || continue
    if [ -r "$f" ]; then dumpf crond "$f"; else printf '@@UNREADABLE\t%s\n' "$f"; fi
  done
fi
for p in hourly daily weekly monthly; do
  d="$R/etc/cron.$p"; [ -d "$d" ] || continue
  for f in "$d"/*; do
    [ -e "$f" ] || continue
    x=0; [ -x "$f" ] && x=1; t=f; [ -d "$f" ] && t=d
    printf '@@PART\t%s\t%s\t%s\t%s\t%s\n' "$p" "$(st "$f")" "$x" "$t" "$f"
  done
done
for d in "$R/etc" "$R/etc/cron"; do
  for f in cron.allow cron.deny; do
    [ -f "$d/$f" ] || continue
    printf '@@LIST\t%s\t%s\n' "$f" "$d/$f"; cat "$d/$f"; printf '\n@@ENDLIST\n'
  done
done
printf '@@PASSWD\n'
if [ -n "$R" ]; then cat "$R/etc/passwd"; else getent passwd 2>/dev/null || cat /etc/passwd; fi
printf '\n@@ENDPASSWD\n'
`
}

func (s *auditState) parseDump(out string) {
	s.passwd = map[string]string{}
	s.lists = map[string][]string{}
	s.listSeen = map[string]bool{}
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		f := strings.Split(l, "\t")
		switch {
		case strings.HasPrefix(l, "@@ID "):
			p := strings.Fields(l)
			if len(p) >= 3 {
				s.asUser = p[1]
				s.isRoot = p[2] == "0"
			}
		case strings.HasPrefix(l, "@@OS "):
			s.os = strings.TrimPrefix(l, "@@OS ")
		case l == "@@DEBIAN":
			s.debian = true
		case strings.HasPrefix(l, "@@DAEMON "):
			s.daemon = strings.TrimPrefix(l, "@@DAEMON ")
		case f[0] == "@@UNREADABLE" && len(f) >= 2:
			s.unread = append(s.unread, f[1])
		case f[0] == "@@FILE" && len(f) >= 4:
			var body []string
			j := i + 1
			for ; j < len(lines) && lines[j] != "@@ENDFILE"; j++ {
				body = append(body, lines[j])
			}
			i = j
			content := strings.Join(body, "\n")
			content = strings.TrimSuffix(content, "\n") // printf '\n' before ENDFILE
			af := &auditFile{kind: f[1], st: parseSt(f[2]), path: strings.Join(f[3:], "\t")}
			af.ct = ParseCrontab(content+"\n", af.kind != "user")
			if af.kind == "user" {
				af.user = path.Base(af.path)
			}
			if !s.seenFile(af.path) {
				s.files = append(s.files, af)
			}
		case f[0] == "@@PART" && len(f) >= 6:
			s.parts = append(s.parts, &auditPart{period: f[1], st: parseSt(f[2]), exec: f[3] == "1", dir: f[4] == "d", path: strings.Join(f[5:], "\t")})
		case f[0] == "@@LIST" && len(f) >= 3:
			j := i + 1
			for ; j < len(lines) && lines[j] != "@@ENDLIST"; j++ {
				if u := strings.TrimSpace(lines[j]); u != "" && !strings.HasPrefix(u, "#") {
					s.lists[f[1]] = append(s.lists[f[1]], u)
				}
			}
			s.listSeen[f[1]] = true
			i = j
		case l == "@@PASSWD":
			j := i + 1
			for ; j < len(lines) && lines[j] != "@@ENDPASSWD"; j++ {
				p := strings.Split(lines[j], ":")
				if len(p) >= 6 && !strings.HasPrefix(p[0], "#") {
					s.passwd[p[0]] = p[5]
				}
			}
			i = j
		}
	}
}

func (s *auditState) seenFile(p string) bool {
	for _, f := range s.files {
		if f.path == p {
			return true
		}
	}
	return false
}

var debianNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func modeBits(m string) int {
	v, err := strconv.ParseInt(m, 8, 32)
	if err != nil {
		return -1
	}
	return int(v)
}

// analyzeFiles adds file-level problems and builds the job list.
func (s *auditState) analyzeFiles() {
	for _, af := range s.files {
		mode := modeBits(af.st.mode)
		switch af.kind {
		case "user":
			if _, ok := s.passwd[af.user]; !ok {
				af.issues = append(af.issues, Issue{SevFail, "user '" + af.user + "' does not exist; cron ignores this crontab (orphan)",
					[]Fix{{Desc: "remove the orphaned crontab", Cmd: "rm " + shq(af.path), Sudo: true}}})
			}
			if af.st.owner != "?" && af.st.owner != af.user {
				af.issues = append(af.issues, Issue{SevFail, fmt.Sprintf("file is owned by %s, not %s; cron refuses it (WRONG FILE OWNER)", af.st.owner, af.user),
					[]Fix{{Desc: "fix ownership", Cmd: "chown " + shq(af.user) + " " + shq(af.path), Sudo: true}}})
			}
			if mode >= 0 && mode&0o022 != 0 {
				af.issues = append(af.issues, Issue{SevFail, "file is group/world-writable (mode " + af.st.mode + "); cron refuses insecure crontabs",
					[]Fix{{Desc: "restrict permissions", Cmd: "chmod 600 " + shq(af.path), Sudo: true}}})
			}
			if msg := s.cronAccess(af.user); msg != "" {
				af.issues = append(af.issues, Issue{SevWarn, msg, nil})
			}
		case "system", "crond":
			if af.st.owner != "?" && af.st.owner != "root" {
				af.issues = append(af.issues, Issue{SevFail, "owned by " + af.st.owner + ", not root; cron ignores it",
					[]Fix{{Desc: "make root the owner", Cmd: "chown root " + shq(af.path), Sudo: true}}})
			}
			if mode >= 0 && mode&0o022 != 0 {
				af.issues = append(af.issues, Issue{SevFail, "group/world-writable (mode " + af.st.mode + "); cron ignores it",
					[]Fix{{Desc: "restrict permissions", Cmd: "chmod 644 " + shq(af.path), Sudo: true}}})
			}
			if af.kind == "crond" && s.debian && !debianNameRe.MatchString(path.Base(af.path)) {
				af.issues = append(af.issues, Issue{SevFail, "file name contains characters other than letters, digits, '_' and '-'; Debian/Ubuntu cron silently ignores it",
					[]Fix{{Desc: "rename it", Cmd: "mv " + shq(af.path) + " " + shq(path.Join(path.Dir(af.path), debianSafe(path.Base(af.path)))), Sudo: true}}})
			}
		}
		for _, l := range af.ct.Lines {
			if l.Kind == KInvalid {
				aj := &auditJob{file: af, line: l, issues: []Issue{{SevFail, "line cannot be parsed by cron: " + l.Err, nil}}}
				af.jobs = append(af.jobs, aj)
			}
			if l.Kind != KJob {
				continue
			}
			aj := &auditJob{file: af, line: l, runUser: af.user}
			if af.kind != "user" {
				aj.runUser = l.User
			}
			home, ok := s.passwd[aj.runUser]
			if !ok {
				if af.kind != "user" {
					aj.issues = append(aj.issues, Issue{SevFail, "runs as user '" + aj.runUser + "', which does not exist", nil})
				}
			} else {
				env := af.ct.EnvAt(l, home)
				aj.ext = ExtractTargets(l.Command, env)
			}
			af.jobs = append(af.jobs, aj)
		}
	}
	sort.SliceStable(s.files, func(i, j int) bool {
		order := map[string]int{"user": 0, "system": 1, "crond": 2}
		if order[s.files[i].kind] != order[s.files[j].kind] {
			return order[s.files[i].kind] < order[s.files[j].kind]
		}
		return s.files[i].path < s.files[j].path
	})
	for _, p := range s.parts {
		name := path.Base(p.path)
		if strings.HasPrefix(name, ".") || p.dir {
			continue
		}
		if s.debian && !debianNameRe.MatchString(name) {
			p.issues = append(p.issues, Issue{SevFail, "ignored by run-parts: name may only contain letters, digits, '_' and '-' (Debian/Ubuntu)",
				[]Fix{{Desc: "rename it", Cmd: "mv " + shq(p.path) + " " + shq(path.Join(path.Dir(p.path), debianSafe(name))), Sudo: true}}})
		}
	}
}

func debianSafe(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	return regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(name, "-")
}

// cronAccess applies cron.allow / cron.deny rules.
func (s *auditState) cronAccess(user string) string {
	if user == "root" {
		return ""
	}
	if s.listSeen["cron.allow"] {
		if !contains(s.lists["cron.allow"], user) {
			return "user is not listed in cron.allow; cron may refuse to run or edit this crontab"
		}
		return ""
	}
	if s.listSeen["cron.deny"] && contains(s.lists["cron.deny"], user) {
		return "user is listed in cron.deny; cron may refuse to run or edit this crontab"
	}
	return ""
}

// checkScript builds the phase-2 script: each user's targets checked as that user.
func (s *auditState) checkScript(byUser map[string][]int, reqs []CheckReq) string {
	var b strings.Builder
	// The checker goes into a world-readable temp file so each user can run it
	// (passing it via $(cat <<EOF) trips bash's parser on "case ... )" patterns).
	hs, body := heredoc(checkFuncs + "\nrun_checks\n")
	b.WriteString(`CHKF=$(mktemp /tmp/cronman.XXXXXX) || exit 1
trap 'rm -f "$CHKF"' EXIT
cat > "$CHKF" ` + hs + "\n" + body + `chmod 644 "$CHKF"
run_as() {
  if [ "$(id -un)" = "$1" ]; then sh "$CHKF"
  elif [ "$(id -u)" != 0 ]; then echo "@@NOSWITCH"; sh "$CHKF"
  elif command -v runuser >/dev/null 2>&1; then runuser -u "$1" -- sh "$CHKF"
  elif command -v sudo >/dev/null 2>&1; then sudo -n -u "$1" sh "$CHKF"
  else su -s /bin/sh "$1" -c "sh $CHKF"
  fi
}
`)
	users := make([]string, 0, len(byUser))
	for u := range byUser {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		var in strings.Builder
		for _, i := range byUser[u] {
			r := reqs[i]
			if strings.ContainsAny(r.Path+r.PathVar, "\t\n") {
				continue
			}
			fmt.Fprintf(&in, "%d\t%c\t%s\t%s\n", i, r.Kind, r.Path, r.PathVar)
		}
		hs, body := heredoc(in.String())
		fmt.Fprintf(&b, "printf '@@G\\t%%s\\t%%s\\n' %s \"$(id -Gn %s 2>/dev/null)\"\n", shq(u), shq(u))
		b.WriteString("run_as " + shq(u) + " " + hs + "\n" + body)
	}
	return b.String()
}

func runAudit(r Runner, opts auditOpts) (int, error) {
	s := &auditState{r: r, opts: opts, groups: map[string][]string{}}
	if err := s.elevate(); err != nil {
		return 2, err
	}
	fmt.Fprintf(os.Stderr, "Collecting crontabs on %s…\n", r.Label())
	out, stderrOut, err := s.run(s.dumpScript())
	if !strings.Contains(out, "@@PASSWD") {
		msg := strings.TrimSpace(stderrOut)
		if err != nil && msg == "" {
			msg = err.Error()
		}
		return 2, fmt.Errorf("could not read cron configuration: %s", msg)
	}
	s.parseDump(out)
	s.analyzeFiles()

	// Phase 2: check every target as the user the job runs as.
	var reqs []CheckReq
	reqIdx := map[string]int{}
	byUser := map[string][]int{}
	addReq := func(u string, rq CheckReq) {
		k := u + "\x00" + rq.key()
		if _, ok := reqIdx[k]; ok {
			return
		}
		reqIdx[k] = len(reqs)
		reqs = append(reqs, rq)
		byUser[u] = append(byUser[u], len(reqs)-1)
	}
	for _, af := range s.files {
		for _, aj := range af.jobs {
			if aj.ext == nil {
				continue
			}
			for _, t := range aj.ext.Targets {
				addReq(aj.runUser, reqFor(t))
			}
		}
	}
	partTarget := func(p *auditPart) Target {
		return Target{Kind: TExec, Role: "command", Word: p.path, Path: p.path, PathVar: "/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/sbin:/usr/bin"}
	}
	for _, p := range s.parts {
		if !p.dir && !strings.HasPrefix(path.Base(p.path), ".") {
			addReq("root", reqFor(partTarget(p)))
		}
	}
	results := map[int]CheckRes{}
	if len(reqs) > 0 {
		fmt.Fprintf(os.Stderr, "Checking %d file(s) as %d user(s)…\n", len(reqs), len(byUser))
		out, stderrOut, err := s.run(s.checkScript(byUser, reqs))
		if err != nil && out == "" {
			return 2, fmt.Errorf("checks failed: %v %s", err, strings.TrimSpace(stderrOut))
		}
		results = parseCheckOutput(out)
		for _, l := range strings.Split(out, "\n") {
			if l == "@@NOSWITCH" {
				s.noSwitch = true
			}
			if f := strings.Split(l, "\t"); f[0] == "@@G" && len(f) >= 3 {
				s.groups[f[1]] = strings.Fields(f[2])
			}
		}
	}
	ctxFor := func(u string) UserCtx {
		return UserCtx{User: u, Groups: s.groups[u], Admin: true, BSD: s.os == "Darwin" || strings.HasSuffix(s.os, "BSD")}
	}
	for _, af := range s.files {
		for _, aj := range af.jobs {
			if aj.ext == nil {
				continue
			}
			jr := &JobReport{Notes: aj.ext.Notes}
			for _, t := range aj.ext.Targets {
				i := reqIdx[aj.runUser+"\x00"+reqFor(t).key()]
				jr.Targets = append(jr.Targets, Analyze(t, results[i], ctxFor(aj.runUser)))
			}
			aj.rep = jr
		}
	}
	for _, p := range s.parts {
		if p.dir || strings.HasPrefix(path.Base(p.path), ".") {
			continue
		}
		t := partTarget(p)
		tr := Analyze(t, results[reqIdx["root\x00"+reqFor(t).key()]], ctxFor("root"))
		if !p.exec {
			tr.Issues = append([]Issue{{SevFail, "not executable; run-parts silently skips it",
				[]Fix{{Desc: "make it executable", Cmd: "chmod +x " + shq(p.path), Sudo: true}}}}, filterNotExec(tr.Issues)...)
		}
		p.rep = &tr
	}
	return s.report(), nil
}

// filterNotExec drops the generic "not executable" issue (replaced by the run-parts one).
func filterNotExec(is []Issue) []Issue {
	var out []Issue
	for _, i := range is {
		if strings.Contains(i.Msg, "not executable by") || strings.Contains(i.Msg, "neither executable") {
			continue
		}
		out = append(out, i)
	}
	return out
}

// elevate decides how to get root on the target.
func (s *auditState) elevate() error {
	out, _, err := RunScript(s.r, "", `id -u; if command -v sudo >/dev/null 2>&1; then sudo -n true 2>/dev/null && echo NOPASS || echo NEEDPASS; else echo NOSUDO; fi`)
	if err != nil && out == "" {
		return err
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return fmt.Errorf("unexpected response: %q", out)
	}
	switch {
	case f[0] == "0":
		return nil
	case f[1] == "NOPASS":
		s.prefix = "sudo -n"
		return nil
	case f[1] == "NOSUDO":
		fmt.Fprintln(os.Stderr, yellow("warning: not root and sudo is not available; only readable crontabs are checked, all as the login user."))
		return nil
	}
	pw, err := readPassword("[sudo] password on " + s.r.Label() + " (empty = continue without root): ")
	if err != nil {
		return err
	}
	if pw == "" {
		fmt.Fprintln(os.Stderr, yellow("continuing without root: only readable crontabs are checked, all as the login user."))
		return nil
	}
	// Verify the password before using it, so script text is never consumed as a password.
	out, _, _ = s.r.Exec("sudo -k -S -p '' sh -c 'echo SUDO_OK'", pw+"\n")
	if !strings.Contains(out, "SUDO_OK") {
		return fmt.Errorf("sudo authentication failed on %s", s.r.Label())
	}
	// -k: always read the password line, even if sudo has cached credentials,
	// so the password can never fall through to the shell as a command.
	s.prefix = "sudo -k -S -p ''"
	s.stdinPre = pw + "\n"
	return nil
}

// ---- reporting ---------------------------------------------------------

func (s *auditState) fixText(f Fix) string {
	if !f.Sudo || s.isRoot {
		return f.Cmd
	}
	if strings.ContainsAny(f.Cmd, "&;|") {
		return "sudo sh -c " + shq(f.Cmd)
	}
	return "sudo " + f.Cmd
}

func (s *auditState) printIssue(indent string, is Issue, pending *[]Fix) {
	fmt.Printf("%s%s %s\n", indent, sevMark(is.Sev), is.Msg)
	for i, f := range is.Fixes {
		lead := "fix:"
		if i > 0 {
			lead = " or:"
		}
		if f.Cmd != "" {
			fmt.Printf("%s    %s %s  %s\n", indent, dim(lead), cyan(s.fixText(f)), dim("# "+f.Desc))
			if i == 0 && is.Sev >= SevWarn && pending != nil {
				*pending = append(*pending, f)
			}
		} else if f.EditFrom != "" {
			fmt.Printf("%s    %s %s\n", indent, dim(lead), "edit the crontab: replace "+f.EditFrom+" with "+f.EditTo)
		} else {
			fmt.Printf("%s    %s %s\n", indent, dim(lead), f.Desc)
		}
	}
}

func worst(is []Issue) Severity {
	s := SevOK
	for _, i := range is {
		if i.Sev > s {
			s = i.Sev
		}
	}
	return s
}

func (aj *auditJob) sev() Severity {
	s := worst(aj.issues)
	if aj.rep != nil {
		if v := aj.rep.Sev(); v > s {
			s = v
		}
	}
	return s
}

func (s *auditState) report() int {
	who := s.asUser
	if s.isRoot {
		who = "root"
	}
	fmt.Printf("%s %s  %s\n", inv(bold(" cronman audit ")), bold(s.r.Label()), dim("(reading as "+who+")"))
	switch s.daemon {
	case "running":
		fmt.Println(dim(" cron daemon: running"))
	case "not-running":
		fmt.Println(" " + red("✗ no cron/crond process is running — no jobs will run at all"))
	}
	if !s.isRoot {
		fmt.Println(" " + yellow("! not running as root: crontabs of other users may be missing and their checks run as "+s.asUser))
	}
	for _, u := range s.unread {
		fmt.Println(" " + yellow("! cannot read "+u))
	}
	fmt.Println()

	var pending []Fix
	nFiles, nJobs, nDisabled, nFail, nWarn := 0, 0, 0, 0, 0
	for _, af := range s.files {
		nFiles++
		var shown []*auditJob
		for _, aj := range af.jobs {
			if aj.line.Kind == KJob {
				nJobs++
			}
			if aj.line.Disabled {
				nDisabled++
				if s.opts.verbose {
					shown = append(shown, aj)
				}
				continue
			}
			switch aj.sev() {
			case SevFail:
				nFail++
			case SevWarn:
				nWarn++
			}
			if s.opts.verbose || aj.sev() >= SevWarn {
				shown = append(shown, aj)
			}
		}
		if worst(af.issues) >= SevWarn {
			if worst(af.issues) == SevFail {
				nFail++
			} else {
				nWarn++
			}
		}
		if len(shown) == 0 && len(af.issues) == 0 && !s.opts.verbose {
			continue
		}
		title := "user " + af.user
		switch af.kind {
		case "system":
			title = "system crontab"
		case "crond":
			title = "cron.d"
		}
		fmt.Printf("%s %s\n", bold("== "+title), dim(af.path))
		for _, is := range af.issues {
			s.printIssue("   ", is, &pending)
		}
		if len(af.ct.Jobs()) == 0 && s.opts.verbose {
			fmt.Println(dim("   (no jobs)"))
		}
		for _, aj := range shown {
			s.printJob(aj, &pending)
		}
		fmt.Println()
	}

	var partsShown []*auditPart
	for _, p := range s.parts {
		if p.rep == nil {
			continue
		}
		nJobs++
		sev := worst(p.issues)
		if v := p.rep.Sev(); v > sev {
			sev = v
		}
		switch sev {
		case SevFail:
			nFail++
		case SevWarn:
			nWarn++
		}
		if s.opts.verbose || sev >= SevWarn {
			partsShown = append(partsShown, p)
		}
	}
	if len(partsShown) > 0 {
		fmt.Printf("%s %s\n", bold("== run-parts scripts"), dim("/etc/cron.{hourly,daily,weekly,monthly}, run as root"))
		for _, p := range partsShown {
			sev := worst(p.issues)
			if v := p.rep.Sev(); v > sev {
				sev = v
			}
			fmt.Printf("   %s %-8s %s\n", sevMark(sev), p.period, p.path)
			for _, is := range p.issues {
				s.printIssue("        ", is, &pending)
			}
			for _, is := range p.rep.Issues {
				if is.Sev >= SevWarn || s.opts.verbose {
					s.printIssue("        ", is, &pending)
				}
			}
		}
		fmt.Println()
	}

	sum := fmt.Sprintf("%d crontab file(s), %d job(s) (%d disabled, not counted)", nFiles, nJobs, nDisabled)
	res := green("no problems found")
	if nFail+nWarn > 0 {
		res = ""
		if nFail > 0 {
			res = red(fmt.Sprintf("%d failing", nFail))
		}
		if nWarn > 0 {
			if res != "" {
				res += ", "
			}
			res += yellow(fmt.Sprintf("%d warning(s)", nWarn))
		}
	}
	fmt.Printf("%s %s — %s\n", bold("Summary:"), sum, res)
	if !s.opts.verbose {
		fmt.Println(dim("Only problems are listed; use -v to list every job."))
	}
	if s.noSwitch {
		fmt.Println(yellow("Note: checks for other users ran as " + s.asUser + " (no root), so results for them are approximate."))
	}

	if s.opts.fix && len(pending) > 0 {
		s.applyFixes(pending)
	}
	if nFail > 0 {
		return 1
	}
	return 0
}

func (s *auditState) printJob(aj *auditJob, pending *[]Fix) {
	l := aj.line
	st := ""
	if l.Disabled {
		st = dim("(disabled) ")
	}
	who := ""
	if aj.file.kind != "user" {
		who = dim("[" + aj.runUser + "] ")
	}
	sched := l.Schedule
	if l.Kind == KInvalid {
		sched = ""
	}
	cmd := l.Command
	if l.Kind == KInvalid {
		cmd = l.Raw
	}
	fmt.Printf("   %s %s %s%s%-13s %s\n", sevMark(aj.sev()), dim(fmt.Sprintf("line %-4d", l.Num)), st, who, sched, fit(cmd, 90))
	for _, is := range aj.issues {
		s.printIssue("        ", is, pending)
	}
	if aj.rep == nil {
		return
	}
	for _, tr := range aj.rep.Targets {
		for _, is := range tr.Issues {
			if is.Sev >= SevWarn || s.opts.verbose {
				var p *[]Fix
				if !l.Disabled {
					p = pending
				}
				s.printIssue("        ", is, p)
			}
		}
	}
	if s.opts.verbose {
		for _, n := range aj.rep.Notes {
			fmt.Println("        " + dim("· "+n))
		}
	}
}

func (s *auditState) applyFixes(fixes []Fix) {
	seen := map[string]bool{}
	fmt.Println()
	fmt.Println(bold("Apply fixes") + dim(" (y = run, n = skip, q = stop)"))
	for _, f := range fixes {
		if seen[f.Cmd] {
			continue
		}
		seen[f.Cmd] = true
		ans, err := readLine(fmt.Sprintf("  %s  %s ? [y/N/q] ", cyan(s.fixText(f)), dim("# "+f.Desc)))
		if err != nil {
			return
		}
		switch strings.ToLower(strings.TrimSpace(ans)) {
		case "y", "yes":
			out, stderrOut, err := s.run(f.Cmd + "\n")
			if err != nil {
				fmt.Println("   " + red("failed: "+oneLine(stderrOut+" "+out+" "+err.Error())))
			} else {
				fmt.Println("   " + green("done"))
			}
		case "q":
			return
		}
	}
	fmt.Println(dim("Run the audit again to confirm."))
}
