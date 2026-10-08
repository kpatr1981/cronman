# cronman

Manage and audit cron jobs on remote hosts over SSH.

```
cronman [options] [user@]host           # interactive editor for that user's crontab
cronman --audit [options] [user@]host   # check every user's cron jobs (needs root or sudo)
cronman --local [--audit]                # this machine instead of SSH (macOS/Linux)
```

## Interactive mode

`cronman alice@web1` logs in as `alice`, lists every job in her crontab (active and
commented-out) with a check status, and lets you:

| key | action |
|---|---|
| `↑ ↓` / `j k` | move |
| `space` / `t` | comment out / un-comment the job |
| `e` | edit the schedule (validated, shows the next run times) |
| `c` | edit the whole command |
| `p` | change only the script/executable path |
| `x` | run the job now, as cron would (asks first; see below) |
| `enter` | details: every file the job uses, problems, numbered fixes |
| `1-9` (details) | apply a proposed fix (asks first) |
| `w` | save (shows a diff, refuses if the crontab changed meanwhile, backs up to `~/.cronman_backups/`) |
| `d` / `r` / `R` | diff / reload / re-run checks |
| `?` / `q` | help / quit |

The key bindings are always shown at the bottom of the screen.

### What is checked (as the job's user, with cron's environment)

* the script/binary exists, symlinks are not broken, every parent directory can be entered
* execute bit (and read bit for scripts; read only for `bash x.sh`, `python x.py`, …)
* `#!` interpreter exists; `#!/usr/bin/env node` finds `node` in **cron's** PATH
  (`/usr/bin:/bin` unless the crontab sets `PATH=`)
* bare commands (`node`, `php`, …) are in cron's PATH — and where they are if not
* Windows CRLF line endings (`bad interpreter: /bin/bash^M`)
* filesystem mounted `noexec`
* output redirects (`>> /var/log/x.log`) and `flock` lock files can be written
* `cd /dir && ...` directories, and relative paths (cron starts jobs in `$HOME`)

Wrappers such as `nice`, `ionice`, `timeout`, `flock`, `env`, `nohup`, `sh -c '…'`
are looked through. Paths built from `$(...)` or unknown variables are reported as not checked.

Fixes are the least invasive change: `chmod u+x` when the user owns the file (run directly),
otherwise `sudo chmod g+x` / `o+x` or `chown` (run through `ssh -t … sudo`, so you type
your sudo password in the terminal).

### Running a job now

`x` (in the list or the details screen) shows the command and asks how to run it, as the
crontab's user and with **cron's** environment rather than your login shell's: environment
cleared, only the crontab's `NAME=value` lines plus `HOME`, `LOGNAME`, `USER`, `SHELL`
and `PATH=/usr/bin:/bin` (unless set in the crontab), run with `$SHELL -c`, working directory `$HOME`,
text after an unescaped `%` fed on stdin, stdout and stderr merged into a pipe. That is the
quickest way to reproduce a "works in my shell, fails in cron" problem.

* `y` runs it in the foreground: output streams live, then the exit status and run time
  are shown. `ctrl-c` stops the job (cronman keeps running).
* `b` starts it detached with `nohup` and returns at once; output goes to
  `~/.cronman_runs/job<N>-<date>.log` on the host.

It runs what is on screen, so unsaved edits and disabled jobs can be tried before saving.

### Commented-out jobs vs comments

`#`, `##`, `#   ` … followed by a valid schedule and a command is a **disabled job**
(enabling it removes the whole prefix). Prose such as `## test this later`, `# m h dom mon dow command`,
and the example lines in Debian/Ubuntu crontab headers stay comments.

## Audit mode

`cronman --audit admin@web1` (root, passwordless sudo, or you are asked for the sudo
password once) reads every user crontab (`/var/spool/cron/crontabs`, `/var/spool/cron`, …),
`/etc/crontab`, `/etc/cron.d/*` and `/etc/cron.{hourly,daily,weekly,monthly}` and checks
each job **as the user it runs as**. Also reported:

* orphaned crontabs (user no longer exists), wrong owner, group/world-writable crontab files
* `cron.d` files and run-parts scripts with a `.` in the name (silently ignored on Debian/Ubuntu)
* run-parts scripts without the execute bit
* users blocked by `cron.allow` / `cron.deny`; cron daemon not running

Only problems are printed (`-v` lists everything). `--fix` offers to run each fix.
Exit status: 0 = no failing jobs, 1 = failing jobs, 2 = error.

## Authentication

cronman does not implement SSH itself. It runs your system `ssh`, so every login method
`ssh` supports works:

* **SSH keys:** default keys, `-i ~/.ssh/id_ed25519`, ssh-agent, hardware keys
* **passwords and key passphrases:** prompted by `ssh` in your terminal
* **`~/.ssh/config`:** host aliases, `User`, `Port`, `ProxyJump`, `IdentityFile` all apply
  (`cronman web1` works if `web1` is a Host entry)
* `-p PORT`, `-i KEY`, `-J JUMPHOST` and `-o Option=value` are passed straight to `ssh`

On macOS/Linux the connection is opened once and reused (`ControlMaster`), so a password
or passphrase is asked for only once per run. The sudo password (audit mode, sudo fixes)
is separate and is asked for on the remote host.

## Platforms

The client runs on **macOS, Linux and Windows** (uses the system `ssh`; Windows 10+
ships OpenSSH). The target must be a Unix host with cron; only POSIX `sh` and standard
tools are needed there — nothing is installed.

On Windows, OpenSSH has no connection sharing, so use key-based auth (or ssh-agent) to
avoid a password prompt per operation. On macOS/Linux one connection is reused.

## Install

Download a prebuilt binary from the
[Releases page](https://github.com/kpatr1981/cronman/releases/latest).
They are static, single-file executables with no dependencies.

| OS | CPU | file |
|---|---|---|
| Linux | x86-64 | `cronman-linux-amd64` |
| Linux | ARM64 (Raspberry Pi 4/5, Graviton) | `cronman-linux-arm64` |
| macOS | Intel | `cronman-darwin-amd64` |
| macOS | Apple Silicon | `cronman-darwin-arm64` |
| Windows | x86-64 | `cronman-windows-amd64.exe` |
| Windows | ARM64 | `cronman-windows-arm64.exe` |

Linux / macOS:

```sh
curl -LO https://github.com/kpatr1981/cronman/releases/latest/download/cronman-linux-amd64
chmod +x cronman-linux-amd64
sudo mv cronman-linux-amd64 /usr/local/bin/cronman
cronman --version
```

Verify the download against `SHA256SUMS` from the same release:

```sh
shasum -a 256 -c SHA256SUMS --ignore-missing
```

On macOS, a binary downloaded with a browser is quarantined by Gatekeeper; clear it with
`xattr -d com.apple.quarantine /usr/local/bin/cronman` (not needed when using `curl`).

## Build from source

You need [Go](https://go.dev/dl/) 1.26 or newer and `git`. There is no cgo, so no C
compiler is needed.

```sh
git clone https://github.com/kpatr1981/cronman.git
cd cronman
go build -o cronman .          # or: make build
./cronman --version
```

Or install straight into `$(go env GOPATH)/bin`:

```sh
go install github.com/kpatr1981/cronman@latest
```

Other targets:

```sh
make test                      # go vet + unit tests
make release                   # dist/ binaries for linux, macOS, windows (amd64/arm64) + SHA256SUMS
make release VERSION=1.1.0     # stamp a version into the binaries
```

Cross-compiling one target by hand:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o cronman-linux-arm64 .
```

## Author

Konstantinos Patronas — <kpatronas@gmail.com>
