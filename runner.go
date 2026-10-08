package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Runner executes POSIX sh scripts on the target machine.
type Runner interface {
	// Exec runs remoteCmd (a sh command line) with stdin; returns stdout/stderr.
	Exec(remoteCmd, stdin string) (string, string, error)
	// Interactive runs remoteCmd attached to the local terminal (for sudo prompts).
	Interactive(remoteCmd string) error
	Label() string
	Close()
}

// RunScript feeds a sh script on stdin to "/bin/sh -s" (optionally behind a prefix such as sudo).
func RunScript(r Runner, prefix, script string) (string, string, error) {
	return r.Exec(strings.TrimSpace(prefix+" /bin/sh -s"), script)
}

type sshRunner struct {
	target   string
	baseArgs []string
	ctlPath  string
}

func newSSHRunner(target, port, identity, jump string, extra []string) *sshRunner {
	r := &sshRunner{target: target}
	if port != "" {
		r.baseArgs = append(r.baseArgs, "-p", port)
	}
	if identity != "" {
		r.baseArgs = append(r.baseArgs, "-i", identity)
	}
	if jump != "" {
		r.baseArgs = append(r.baseArgs, "-J", jump)
	}
	for _, o := range extra {
		r.baseArgs = append(r.baseArgs, "-o", o)
	}
	// Reuse one connection so the password/passphrase is asked only once.
	// Windows' OpenSSH does not support connection multiplexing.
	if runtime.GOOS != "windows" {
		r.ctlPath = "/tmp/cronman-" + randHex(6) + "-%C"
		r.baseArgs = append(r.baseArgs,
			"-o", "ControlMaster=auto",
			"-o", "ControlPath="+r.ctlPath,
			"-o", "ControlPersist=300")
	}
	return r
}

func (r *sshRunner) Label() string { return r.target }

func (r *sshRunner) Exec(remoteCmd, stdin string) (string, string, error) {
	args := append(append([]string{}, r.baseArgs...), "-T", "-o", "LogLevel=ERROR", r.target, remoteCmd)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 255 {
		return out.String(), errb.String(), fmt.Errorf("ssh to %s failed: %s", r.target, strings.TrimSpace(errb.String()))
	}
	return out.String(), errb.String(), err
}

func (r *sshRunner) Interactive(remoteCmd string) error {
	args := append(append([]string{}, r.baseArgs...), "-t", "-o", "LogLevel=ERROR", r.target, remoteCmd)
	cmd := exec.Command("ssh", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (r *sshRunner) Close() {
	if r.ctlPath == "" {
		return
	}
	args := append(append([]string{}, r.baseArgs...), "-O", "exit", r.target)
	cmd := exec.Command("ssh", args...)
	cmd.Stdout, cmd.Stderr = nil, nil
	_ = cmd.Run()
}

// localRunner runs on this machine (for --local; macOS/Linux only).
type localRunner struct{}

func (localRunner) Label() string { return "local" }

func (localRunner) Exec(remoteCmd, stdin string) (string, string, error) {
	cmd := exec.Command("/bin/sh", "-c", remoteCmd)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func (localRunner) Interactive(remoteCmd string) error {
	cmd := exec.Command("/bin/sh", "-c", remoteCmd)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func (localRunner) Close() {}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// heredoc wraps data in a quoted here-document with a delimiter that cannot
// collide with the data.
func heredoc(data string) (start, body string) {
	var mark string
	for {
		mark = "CRONMAN_" + randHex(8)
		if !strings.Contains(data, mark) {
			break
		}
	}
	if data != "" && !strings.HasSuffix(data, "\n") {
		data += "\n"
	}
	return "<<'" + mark + "'", data + mark + "\n"
}

// shq quotes s for a POSIX shell.
func shq(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=@+,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
