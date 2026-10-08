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

## Platforms

The client runs on **macOS, Linux and Windows** (uses the system `ssh`; Windows 10+
ships OpenSSH). The target must be a Unix host with cron; only POSIX `sh` and standard
tools are needed there — nothing is installed.

On Windows, OpenSSH has no connection sharing, so use key-based auth (or ssh-agent) to
avoid a password prompt per operation. On macOS/Linux one connection is reused.

## Build

```
go build -o cronman .
make release        # dist/ binaries for linux, macOS, windows (amd64/arm64)
go test ./...
```
