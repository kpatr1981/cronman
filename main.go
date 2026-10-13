// cronman — manage and audit cron jobs on remote hosts over SSH.
//
// Author: Konstantinos Patronas <kpatronas@gmail.com>
// https://github.com/kpatr1981/cronman
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

var version = "1.3.0"

const (
	author  = "Konstantinos Patronas <kpatronas@gmail.com>"
	repoURL = "https://github.com/kpatr1981/cronman"
)

func stderr() io.Writer { return os.Stderr }

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func usage() {
	fmt.Fprintf(os.Stderr, `cronman %s — manage cron jobs over SSH

Usage:
  cronman [options] [user@]host           interactive editor for that user's crontab
  cronman --audit [options] [user@]host   check every user's cron jobs (needs root or sudo)
  cronman --local [--audit]                work on this machine instead of over SSH
  cronman [options] host1,host2,…          edit a job on several hosts at once (only jobs
                                           whose command path is on every host); also
                                           cronman host1 host2 … or --hosts-file FILE

Interactive keys (also shown on screen):
  ↑/↓ move   space enable/disable   e schedule   c command   p script path   D delete
  x run now   o output   S record result   enter details & fixes   w save   d diff
  r reload   ? help   q quit

Options:
`, version)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Exit status (--audit): 0 no failing jobs, 1 failing jobs found, 2 error.

Author: %s
%s
`, author, repoURL)
}

func main() {
	var (
		audit    = flag.Bool("audit", false, "check all users' cron jobs for missing files and permission problems")
		local    = flag.Bool("local", false, "use this machine instead of SSH (macOS/Linux)")
		port     = flag.String("p", "", "SSH port")
		identity = flag.String("i", "", "SSH identity file")
		jump     = flag.String("J", "", "SSH jump host")
		verbose  = flag.Bool("v", false, "audit: list every job, not only problems")
		fix      = flag.Bool("fix", false, "audit: offer to apply each proposed fix")
		noColor  = flag.Bool("no-color", false, "disable colours")
		showVer  = flag.Bool("version", false, "print version")
		rootPfx  = flag.String("root-prefix", "", "audit: read cron files under this directory (for testing / mounted images)")
		hostsF   = flag.String("hosts-file", "", "read hosts from this file (one per line, # comments) and edit them together")
		sshOpts  multiFlag
	)
	flag.Var(&sshOpts, "o", "extra SSH option, e.g. -o StrictHostKeyChecking=no (repeatable)")
	flag.Usage = usage

	// Allow options after the host too: cronman user@host --audit
	args := os.Args[1:]
	var positional []string
	for {
		if err := flag.CommandLine.Parse(args); err != nil {
			os.Exit(2)
		}
		rest := flag.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}

	if *showVer {
		fmt.Println("cronman", version)
		fmt.Println("Author:", author)
		fmt.Println(repoURL)
		return
	}
	initColor()
	if *noColor {
		useColor = false
	}

	hosts, err := hostList(positional, *hostsF)
	if err != nil {
		fatal(err.Error())
	}
	if len(hosts) > 1 || *hostsF != "" {
		switch {
		case *audit:
			fatal("--audit takes one host; run it once per host")
		case *local:
			fatal("--local cannot be combined with several hosts")
		}
		var runners []Runner
		for _, h := range hosts {
			runners = append(runners, newSSHRunner(h, *port, *identity, *jump, sshOpts))
		}
		err := runMulti(runners)
		for _, r := range runners {
			r.Close()
		}
		if err != nil {
			fatal(err.Error())
		}
		return
	}

	var r Runner
	switch {
	case *local:
		if runtime.GOOS == "windows" {
			fatal("--local is not available on Windows (there is no cron); connect to a host instead")
		}
		r = localRunner{}
	case len(hosts) == 1:
		r = newSSHRunner(hosts[0], *port, *identity, *jump, sshOpts)
	default:
		usage()
		os.Exit(2)
	}
	defer r.Close()

	if *audit {
		code, err := runAudit(r, auditOpts{verbose: *verbose, fix: *fix, root: *rootPfx})
		if err != nil {
			r.Close()
			fatal(err.Error())
		}
		r.Close()
		os.Exit(code)
	}
	if err := runInteractive(r); err != nil {
		r.Close()
		fatal(err.Error())
	}
}

// hostList collects hosts from the arguments (each may be a comma list) and
// from a hosts file, dropping duplicates.
func hostList(args []string, file string) ([]string, error) {
	var raw []string
	for _, a := range args {
		raw = append(raw, strings.Split(a, ",")...)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, l := range strings.Split(string(data), "\n") {
			if i := strings.Index(l, "#"); i >= 0 {
				l = l[:i]
			}
			raw = append(raw, strings.Fields(l)...)
		}
		if len(raw) == 0 {
			return nil, fmt.Errorf("%s lists no hosts", file)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, h := range raw {
		h = strings.TrimSpace(h)
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out, nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, red("cronman: ")+msg)
	os.Exit(2)
}
