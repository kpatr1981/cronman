// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"fmt"
	"path"
	"strings"
)

// checkFuncs is a POSIX sh library. run_checks reads lines
// "id<TAB>kind<TAB>path<TAB>PATH" and prints "id<TAB>key=value<TAB>..." per line.
// All tests run as the invoking user, so they reflect that user's real rights.
const checkFuncs = `
cd / 2>/dev/null
TAB=$(printf '\t')
CR=$(printf '\r')
EXTRA="/usr/local/bin:/usr/local/sbin:/usr/bin:/bin:/usr/sbin:/sbin:/snap/bin:/opt/homebrew/bin:$HOME/bin:$HOME/.local/bin"
st() { stat -L -c '%U:%G:%a' "$1" 2>/dev/null || stat -L -f '%Su:%Sg:%Lp' "$1" 2>/dev/null; }
lookup() {
  _r=""; _o=$IFS; IFS=:; set -f
  for _d in $2; do
    [ -z "$_r" ] && [ -f "$_d/$1" ] && [ -x "$_d/$1" ] && _r="$_d/$1"
  done
  set +f; IFS=$_o
  [ -n "$_r" ] && printf '%s\n' "$_r"
}
emit() { out="$out$TAB$1"; }
run_checks() {
  while IFS="$TAB" read -r id kind p pv; do
    [ -n "$id" ] || continue
    out="$id"
    if [ "$kind" = b ]; then
      f=$(lookup "$p" "$pv")
      if [ -z "$f" ]; then
        emit notfound=1
        e=$(lookup "$p" "$EXTRA"); [ -n "$e" ] && emit "elsewhere=$e"
        printf '%s\n' "$out"; continue
      fi
      emit "resolved=$f"; p=$f; kind=x
    fi
    if [ -L "$p" ] && [ ! -e "$p" ]; then emit "broken_link=$(readlink "$p")"; fi
    cur=""; rest=${p#/}
    while :; do
      case $rest in */*) ;; *) break;; esac
      comp=${rest%%/*}; rest=${rest#*/}
      [ -z "$comp" ] && continue
      cur="$cur/$comp"
      if [ ! -e "$cur" ]; then emit "missing_dir=$cur"; break; fi
      if [ -d "$cur" ] && [ ! -x "$cur" ]; then emit "baddir=$cur"; emit "baddir_st=$(st "$cur")"; break; fi
    done
    if [ -e "$p" ]; then
      emit exists=1
      if [ -d "$p" ]; then emit type=d; elif [ -f "$p" ]; then emit type=f; else emit type=o; fi
      [ -r "$p" ] && emit r=1
      [ -w "$p" ] && emit w=1
      [ -x "$p" ] && emit x=1
      emit "st=$(st "$p")"
      if [ -f "$p" ] && [ -r "$p" ]; then
        first=$(head -c 256 "$p" 2>/dev/null | head -n 1 | tr -d '\000')
        case $first in
          "#!"*)
            emit shebang=1
            line=${first#??}
            case $line in *"$CR") emit crlf=1; line=${line%"$CR"};; esac
            set -f; set -- $line; set +f
            interp=$1; iarg=$2; [ "$iarg" = -S ] && iarg=$3
            emit "interp=$interp"
            [ -f "$interp" ] && [ -x "$interp" ] && emit interp_ok=1
            case $interp in
              */env)
                if [ -n "$iarg" ]; then
                  emit "envcmd=$iarg"
                  f=$(lookup "$iarg" "$pv")
                  if [ -n "$f" ]; then emit "envcmd_found=$f"
                  else e=$(lookup "$iarg" "$EXTRA"); [ -n "$e" ] && emit "envcmd_elsewhere=$e"; fi
                fi;;
            esac;;
          "$(printf '\177')ELF"*|"$(printf '\317\372\355\376')"*|"$(printf '\312\376\272\276')"*) emit elf=1;;
          *) [ "$(head -c 512 "$p" | tr -d '\000' | wc -c)" -lt "$(head -c 512 "$p" | wc -c)" ] && emit elf=1;;
        esac
        case $kind in x|r)
          if [ "$kind" = r ] || [ "$(printf '%s' "$first" | cut -c1-2)" = "#!" ]; then
            n=$(grep -c "$CR" "$p" 2>/dev/null); [ "${n:-0}" -gt 0 ] && emit "crlf_lines=$n"
          fi;;
        esac
      fi
      if command -v findmnt >/dev/null 2>&1; then
        o=$(findmnt -n -o OPTIONS -T "$p" 2>/dev/null)
      elif [ -r /proc/mounts ]; then
        # no findmnt (Alpine/busybox): options of the longest mount point containing $p
        rp=$(readlink -f "$p" 2>/dev/null) || rp=$p
        o=$(awk -v p="$rp" '{ m = $2; pre = (m == "/") ? "/" : m "/"
          if ((p == m || index(p, pre) == 1) && length(m) >= bl) { bl = length(m); bo = $4 } } END { print bo }' /proc/mounts)
      fi
      case ",$o," in *,noexec,*) emit noexec=1;; esac
    else
      emit exists=0
      d=$(dirname "$p")
      emit "parent=$d"
      [ -d "$d" ] && emit parent_exists=1
      [ -d "$d" ] && [ -w "$d" ] && [ -x "$d" ] && emit parent_w=1
      [ -d "$d" ] && emit "parent_st=$(st "$d")"
    fi
    printf '%s\n' "$out"
  done
}
`

// CheckReq is one unique thing to check.
type CheckReq struct {
	Kind    byte
	Path    string
	PathVar string
}

func (c CheckReq) key() string { return string(c.Kind) + "\x00" + c.Path + "\x00" + c.PathVar }

// CheckRes holds the key=value facts reported for one request.
type CheckRes map[string]string

func reqFor(t Target) CheckReq { return CheckReq{t.Kind, t.Path, t.PathVar} }

// checkInput renders requests as the run_checks input.
func checkInput(reqs []CheckReq) string {
	var b strings.Builder
	for i, r := range reqs {
		if strings.ContainsAny(r.Path+r.PathVar, "\t\n") {
			continue
		}
		fmt.Fprintf(&b, "%d\t%c\t%s\t%s\n", i, r.Kind, r.Path, r.PathVar)
	}
	return b.String()
}

// parseCheckOutput maps request index -> facts.
func parseCheckOutput(out string) map[int]CheckRes {
	res := map[int]CheckRes{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		var id int
		if _, err := fmt.Sscanf(parts[0], "%d", &id); err != nil || !isDigits(parts[0]) {
			continue
		}
		m := CheckRes{}
		for _, kv := range parts[1:] {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
		res[id] = m
	}
	return res
}

// RunChecks checks reqs as the logged-in user and returns results keyed by req.key().
func RunChecks(r Runner, reqs []CheckReq) (map[string]CheckRes, error) {
	out := map[string]CheckRes{}
	if len(reqs) == 0 {
		return out, nil
	}
	hs, body := heredoc(checkInput(reqs))
	script := checkFuncs + "\nrun_checks " + hs + "\n" + body
	stdout, stderr, err := RunScript(r, "", script)
	if err != nil && stdout == "" {
		return nil, fmt.Errorf("check failed: %v %s", err, strings.TrimSpace(stderr))
	}
	for i, m := range parseCheckOutput(stdout) {
		if i < len(reqs) {
			out[reqs[i].key()] = m
		}
	}
	return out, nil
}

// ---- analysis ----------------------------------------------------------

type Severity int

const (
	SevOK Severity = iota
	SevInfo
	SevWarn
	SevFail
)

// Fix is a proposed remedy. Exactly one of Cmd (shell, run on the target) or
// EditFrom/EditTo (rewrite the cron command) is set; Hint-only fixes have neither.
type Fix struct {
	Desc     string
	Cmd      string
	Sudo     bool
	EditFrom string
	EditTo   string
}

type Issue struct {
	Sev   Severity
	Msg   string
	Fixes []Fix
}

type TargetReport struct {
	T       Target
	Res     CheckRes
	Summary string
	Issues  []Issue
}

func (tr TargetReport) Sev() Severity {
	s := SevOK
	for _, i := range tr.Issues {
		if i.Sev > s {
			s = i.Sev
		}
	}
	return s
}

// UserCtx describes whose rights the checks reflect.
type UserCtx struct {
	User   string
	Groups []string
	BSD    bool // BSD userland (sed -i '')
	Admin  bool // fixes will be run by an admin on the user's behalf (audit mode)
}

type ownerInfo struct{ owner, group, mode string }

func parseSt(s string) ownerInfo {
	p := strings.SplitN(s, ":", 3)
	for len(p) < 3 {
		p = append(p, "?")
	}
	return ownerInfo{p[0], p[1], p[2]}
}

func (o ownerInfo) String() string {
	m := o.mode
	if len(m) == 3 {
		m = "0" + m
	}
	return fmt.Sprintf("owner %s:%s mode %s", o.owner, o.group, m)
}

// permFixes proposes the least-invasive chmod/chown so ctx.User gets bits on p.
func permFixes(p string, oi ownerInfo, bits string, ctx UserCtx, isDir bool) []Fix {
	q := shq(p)
	if oi.owner == ctx.User {
		return []Fix{{Desc: "give the owner (" + ctx.User + ") " + bitsName(bits) + " permission", Cmd: "chmod u+" + bits + " " + q, Sudo: ctx.Admin}}
	}
	var fs []Fix
	if contains(ctx.Groups, oi.group) {
		fs = append(fs, Fix{Desc: "give group " + oi.group + " (which " + ctx.User + " is in) " + bitsName(bits) + " permission", Cmd: "chmod g+" + bits + " " + q, Sudo: true})
	}
	if ctx.User == "root" {
		fs = append(fs, Fix{Desc: "add the " + bitsName(bits) + " bit", Cmd: "chmod +" + bits + " " + q, Sudo: true})
		return fs
	}
	fs = append(fs, Fix{Desc: "give everyone " + bitsName(bits) + " permission", Cmd: "chmod o+" + bits + " " + q, Sudo: true})
	if !isDir {
		fs = append(fs, Fix{Desc: "make " + ctx.User + " the owner", Cmd: "chown " + shq(ctx.User) + " " + q + " && chmod u+" + bits + " " + q, Sudo: true})
	}
	return fs
}

func bitsName(b string) string {
	switch b {
	case "x":
		return "execute"
	case "rx":
		return "read+execute"
	case "r":
		return "read"
	case "w":
		return "write"
	}
	return b
}

func stripCRFix(p string, oi ownerInfo, ctx UserCtx) Fix {
	cmd := "sed -i 's/\\r$//' " + shq(p)
	if ctx.BSD {
		cmd = "sed -i '' \"s/$(printf '\\r')\\$//\" " + shq(p)
	}
	return Fix{Desc: "convert Windows (CRLF) line endings to Unix (LF)", Cmd: cmd, Sudo: ctx.Admin || oi.owner != ctx.User}
}

// Analyze turns raw facts into a verdict, issues and fixes.
func Analyze(t Target, res CheckRes, ctx UserCtx) TargetReport {
	tr := TargetReport{T: t, Res: res}
	add := func(sev Severity, msg string, fixes ...Fix) {
		tr.Issues = append(tr.Issues, Issue{sev, msg, fixes})
	}
	if res == nil {
		add(SevInfo, "not checked")
		return tr
	}
	p := t.Path
	if t.Kind == TBare {
		if res["notfound"] == "1" {
			msg := fmt.Sprintf("'%s' is not found in cron's PATH (%s)", t.Word, t.PathVar)
			if e := res["elsewhere"]; e != "" {
				add(SevFail, msg+"; it exists at "+e,
					Fix{Desc: "use the absolute path " + e + " in the command", EditFrom: t.Word, EditTo: e},
					Fix{Desc: "or add a PATH line at the top of the crontab, e.g. PATH=" + path.Dir(e) + ":/usr/bin:/bin"})
			} else {
				add(SevFail, msg+" or anywhere common; is it installed?")
			}
			return tr
		}
		p = res["resolved"]
		tr.T.Path = p
	}
	oi := parseSt(res["st"])
	if l := res["broken_link"]; l != "" {
		add(SevFail, "broken symlink (points to "+l+", which does not exist)")
		return tr
	}
	if d := res["baddir"]; d != "" {
		doi := parseSt(res["baddir_st"])
		add(SevFail, fmt.Sprintf("directory %s cannot be entered by %s (%s)", d, ctx.User, doi), permFixes(d, doi, "x", ctx, true)...)
		return tr
	}
	if res["exists"] != "1" {
		if t.Kind == TWrite {
			switch {
			case res["parent_exists"] != "1":
				d := res["parent"]
				add(SevFail, "directory "+d+" does not exist, so "+t.Role+" "+p+" cannot be created",
					Fix{Desc: "create the directory", Cmd: "mkdir -p " + shq(d), Sudo: ctx.Admin},
					Fix{Desc: "create it as admin and hand it to " + ctx.User, Cmd: "mkdir -p " + shq(d) + " && chown " + shq(ctx.User) + " " + shq(d), Sudo: true})
			case res["parent_w"] != "1":
				d := res["parent"]
				doi := parseSt(res["parent_st"])
				add(SevFail, fmt.Sprintf("%s cannot be created: %s is not writable by %s (%s)", p, d, ctx.User, doi),
					Fix{Desc: "pre-create the file owned by " + ctx.User, Cmd: "touch " + shq(p) + " && chown " + shq(ctx.User) + " " + shq(p), Sudo: true})
			default:
				tr.Summary = "will be created (directory is writable)"
			}
			return tr
		}
		msg := p + " does not exist"
		if m := res["missing_dir"]; m != "" && m != p {
			msg += " (" + m + " is missing)"
		}
		add(SevFail, msg, Fix{Desc: "correct the path in the cron job (in cronman: select the job and press p)"})
		return tr
	}

	isDir := res["type"] == "d"
	switch t.Kind {
	case TDir:
		if !isDir {
			add(SevFail, p+" is not a directory")
		} else if res["x"] != "1" {
			add(SevFail, fmt.Sprintf("cannot cd into %s (%s)", p, oi), permFixes(p, oi, "x", ctx, true)...)
		} else {
			tr.Summary = "directory accessible"
		}
	case TWrite:
		if isDir {
			add(SevFail, p+" is a directory")
		} else if res["w"] != "1" {
			add(SevFail, fmt.Sprintf("%s is not writable by %s (%s)", p, ctx.User, oi), permFixes(p, oi, "w", ctx, false)...)
		} else {
			tr.Summary = "writable"
		}
	case TRead:
		switch {
		case isDir:
			add(SevFail, p+" is a directory, not a file")
		case res["r"] != "1":
			add(SevFail, fmt.Sprintf("%s is not readable by %s (%s)", p, ctx.User, oi), permFixes(p, oi, "r", ctx, false)...)
		default:
			tr.Summary = "readable (" + oi.String() + ")"
			if n := res["crlf_lines"]; n != "" {
				add(SevWarn, n+" line(s) end with CR (Windows line endings); shell scripts will misbehave", stripCRFix(p, oi, ctx))
			}
		}
	case TExec, TBare:
		if isDir {
			add(SevFail, p+" is a directory")
			break
		}
		script := res["shebang"] == "1"
		noexec := res["noexec"] == "1"
		switch {
		case noexec && res["r"] != "1":
			// The x test always fails on a noexec mount, so only readability matters
			// (for the run-through-the-interpreter workaround below).
			add(SevFail, fmt.Sprintf("%s is not readable by %s (%s)", p, ctx.User, oi), permFixes(p, oi, "r", ctx, false)...)
		case noexec:
		case res["x"] != "1" && res["r"] != "1":
			add(SevFail, fmt.Sprintf("%s is neither executable nor readable by %s (%s)", p, ctx.User, oi), permFixes(p, oi, "rx", ctx, false)...)
		case res["x"] != "1":
			add(SevFail, fmt.Sprintf("%s is not executable by %s (%s)", p, ctx.User, oi), permFixes(p, oi, "x", ctx, false)...)
		case res["r"] != "1" && res["elf"] != "1":
			add(SevFail, fmt.Sprintf("%s is executable but not readable by %s; a script must be readable for its interpreter (%s)", p, ctx.User, oi), permFixes(p, oi, "r", ctx, false)...)
		}
		if noexec {
			add(SevFail, p+" is on a filesystem mounted noexec; it cannot be executed directly",
				Fix{Desc: "run it through its interpreter instead, e.g. /bin/sh " + p})
		}
		if script {
			interp := res["interp"]
			switch {
			case res["crlf"] == "1":
				add(SevFail, "the #! line ends with CR (Windows line endings): you will get 'bad interpreter: "+interp+"^M'", stripCRFix(p, oi, ctx))
			case res["interp_ok"] != "1":
				add(SevFail, "interpreter "+interp+" from the #! line does not exist or is not executable",
					Fix{Desc: "install it, or change the first line of the script to a valid interpreter"})
			case res["envcmd"] != "" && res["envcmd_found"] == "":
				msg := fmt.Sprintf("#!%s %s: '%s' is not in cron's PATH (%s)", interp, res["envcmd"], res["envcmd"], t.PathVar)
				if e := res["envcmd_elsewhere"]; e != "" {
					add(SevFail, msg+"; it exists at "+e,
						Fix{Desc: "add a PATH line at the top of the crontab, e.g. PATH=" + path.Dir(e) + ":/usr/bin:/bin"},
						Fix{Desc: "or change the script's first line to #!" + e})
				} else {
					add(SevFail, msg)
				}
			}
			if n := res["crlf_lines"]; n != "" && res["crlf"] != "1" {
				add(SevWarn, n+" line(s) end with CR (Windows line endings)", stripCRFix(p, oi, ctx))
			}
		} else if res["elf"] != "1" && res["r"] == "1" && t.Role != "interpreter" && t.Role != "wrapper" {
			add(SevWarn, "no #! line and not a binary; it will be run by /bin/sh",
				Fix{Desc: "add a first line such as #!/bin/sh or #!/bin/bash"})
		}
		if len(tr.Issues) == 0 {
			kind := "executable"
			if script {
				kind = "executable script (" + strings.TrimSpace(res["interp"]+" "+res["envcmd"]) + ")"
			}
			tr.Summary = kind + ", " + oi.String()
		}
	}
	return tr
}

// JobReport is the verdict for one cron job.
type JobReport struct {
	Targets []TargetReport
	Notes   []string
}

func (jr *JobReport) Sev() Severity {
	if jr == nil {
		return SevInfo
	}
	s := SevOK
	for _, t := range jr.Targets {
		if v := t.Sev(); v > s {
			s = v
		}
	}
	return s
}

// ApplyEdit replaces the first whole-word occurrence of from in cmd.
func ApplyEdit(cmd, from, to string) (string, bool) {
	isSep := func(c byte) bool { return strings.IndexByte(" \t;&|()<>\"'", c) >= 0 }
	for i := 0; i+len(from) <= len(cmd); i++ {
		if cmd[i:i+len(from)] != from {
			continue
		}
		if (i == 0 || isSep(cmd[i-1])) && (i+len(from) == len(cmd) || isSep(cmd[i+len(from)])) {
			return cmd[:i] + to + cmd[i+len(from):], true
		}
	}
	return cmd, false
}
