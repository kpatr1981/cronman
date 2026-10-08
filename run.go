// cronman — Author: Konstantinos Patronas <kpatronas@gmail.com>

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"time"
)

// cronEnv is the environment cron gives a job: a minimal base, then the
// NAME=value lines that appear above it in the crontab.
func (a *app) cronEnv(l *Line) map[string]string {
	env := a.ct.EnvAt(l, a.info.Home)
	for _, k := range []string{"LOGNAME", "USER"} {
		if _, ok := env[k]; !ok && a.info.User != "" {
			env[k] = a.info.User
		}
	}
	return env
}

// jobInvocation is a sh fragment that starts the job the way cron does:
// cleared environment, "$SHELL -c" on the part before '%', and the text after
// '%' (or /dev/null) as stdin. redir is added to the command (e.g. ">log 2>&1 &").
func (a *app) jobInvocation(l *Line, redir string) string {
	env := a.cronEnv(l)
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("env -i")
	for _, k := range names {
		b.WriteString(" " + shq(k+"="+env[k]))
	}
	b.WriteString(" " + shq(env["SHELL"]) + " -c " + shq(CronShellPart(l.Command)))
	if redir != "" {
		b.WriteString(" " + redir)
	}
	if in, ok := CronStdin(l.Command); ok {
		hs, body := heredoc(in)
		// The here-document body must start on the line after the command.
		cmd := b.String()
		if strings.HasSuffix(cmd, "&") {
			return strings.TrimSuffix(cmd, "&") + hs + " &\n" + body
		}
		return cmd + " " + hs + "\n" + body
	}
	if strings.HasSuffix(b.String(), "&") {
		return strings.TrimSuffix(b.String(), "&") + "</dev/null &\n"
	}
	return b.String() + " </dev/null\n"
}

func (a *app) cdHome(l *Line) string {
	return "cd " + shq(a.cronEnv(l)["HOME"]) + " 2>/dev/null || cd /\n"
}

// foregroundScript runs the job with stdout+stderr merged into a pipe (as cron
// does) and exits with the job's exit status.
func (a *app) foregroundScript(l *Line) string {
	return a.cdHome(l) +
		`rcf=$(mktemp "${TMPDIR:-/tmp}/cronman.XXXXXX") || exit 125
trap 'rm -f "$rcf"; exit 130' INT TERM HUP
( ` + a.jobInvocation(l, "") + `echo $? >"$rcf" ) 2>&1 | cat
rc=$(cat "$rcf" 2>/dev/null); rm -f "$rcf"
[ -n "$rc" ] || rc=130
exit "$rc"
`
}

// backgroundScript starts the job detached with nohup, logging to a file.
func (a *app) backgroundScript(l *Line, n int) string {
	return `d="$HOME/.cronman_runs"
mkdir -p "$d" || exit 1
log="$d/job` + fmt.Sprint(n) + `-$(date +%Y%m%d-%H%M%S).log"
( umask 077; : >"$log" ) || exit 1
` + a.cdHome(l) + "nohup " + a.jobInvocation(l, `>>"$log" 2>&1 &`) + `echo "@@PID $!"
echo "@@LOG $log"
`
}

// runNow runs the selected job immediately, after confirmation.
func (a *app) runNow(base func([]string) []string) {
	l := a.current()
	if l == nil {
		return
	}
	if base == nil {
		base = a.render
	}
	n := a.sel + 1
	var notes []string
	if l.Disabled {
		notes = append(notes, "disabled")
	}
	if l.Modified {
		notes = append(notes, "with unsaved edits")
	}
	if a.rep[l].Sev() == SevFail {
		notes = append(notes, "checks failing")
	}
	what := fmt.Sprintf("Run job %d", n)
	if len(notes) > 0 {
		what += " (" + strings.Join(notes, ", ") + ")"
	}
	q := what + " now on " + a.r.Label() + " as " + a.info.User + ": " + l.Command
	switch a.ask(base, q, "y = run and watch output   b = run in background (nohup, log file)   any other key = cancel") {
	case 'y', 'Y':
		a.runForeground(l, n)
	case 'b', 'B':
		a.runBackground(l, n)
	default:
		a.status = "Cancelled."
	}
}

func (a *app) runForeground(l *Line, n int) {
	a.scr.suspend()
	fmt.Printf("\n%s job %d on %s as %s, with cron's environment\n$ %s\n%s\n\n",
		bold("Running"), n, a.r.Label(), a.info.User, l.Command, dim("(stdout and stderr merged, as cron mails them; ctrl-c stops the job)"))

	// The job gets ctrl-c (directly, or through ssh's terminal); cronman must not die.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	start := time.Now()
	err := a.r.Interactive("sh -c " + shq(a.foregroundScript(l)))
	took := time.Since(start).Round(100 * time.Millisecond)
	signal.Stop(sig)

	code := 0
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		code = ee.ExitCode()
	default:
		fmt.Printf("\n%s %v\n", red("could not run:"), err)
		a.status = red("Run failed: " + err.Error())
		code = -1
	}
	if code >= 0 {
		var res string
		switch {
		case code == 0:
			res = green("exit status 0")
		case code == 130:
			res = yellow("interrupted (exit status 130)")
		default:
			res = red(fmt.Sprintf("exit status %d", code))
		}
		fmt.Printf("\n%s after %s\n", res, took)
		a.status = fmt.Sprintf("Job %d finished: %s after %s.", n, res, took)
	}
	fmt.Print("Press Enter to return to cronman…")
	_, _ = readLine("")
	a.scr.resume()
}

func (a *app) runBackground(l *Line, n int) {
	out, stderr, err := RunScript(a.r, "", a.backgroundScript(l, n))
	pid, log := "", ""
	for _, ln := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(ln, "@@PID "); ok {
			pid = v
		}
		if v, ok := strings.CutPrefix(ln, "@@LOG "); ok {
			log = v
		}
	}
	if pid == "" {
		msg := oneLine(stderr)
		if msg == "" && err != nil {
			msg = err.Error()
		}
		a.status = red("Could not start job: " + msg)
		return
	}
	a.status = fmt.Sprintf("Job %d started in the background (pid %s); output → %s", n, pid, log)
}
