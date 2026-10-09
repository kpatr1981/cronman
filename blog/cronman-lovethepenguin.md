---
title: "cronman: Stop Guessing Why Your Cron Job Didn't Run"
author: Konstantinos Patronas
email: kpatronas@gmail.com
date: 2026-10-08
tags: [linux, cron, ssh, sysadmin, golang, open-source]
---

# cronman: Stop Guessing Why Your Cron Job Didn't Run

*By Konstantinos Patronas, kpatronas@gmail.com*

Every Linux admin has had this conversation:

> "The backup script works perfectly when I run it."
> "So why is last night's backup missing?"

The script runs fine in your shell and does nothing under cron. The cause is nearly always
one of the same few things. The script lost its execute bit. Someone edited it on Windows
and now it has CRLF line endings. `node` lives in `/usr/local/bin`, and cron's `PATH` is only
`/usr/bin:/bin`. The log directory in `>> /var/log/app/backup.log` doesn't exist. Or
the user who owned the crontab left the company two years ago.

Cron reports none of this. At best you get a mail to a local mailbox nobody reads.

I wrote **cronman** to find these problems before they bite. It's a small, single-binary
tool that connects to a host over SSH, reads the crontabs, and checks every job **as the
user it runs as, with cron's environment**. It also gives you a friendly terminal editor for
crontabs, so you don't need `crontab -e` and `vi` on a box you barely know.

- **Repository:** <https://github.com/kpatr1981/cronman>
- **Downloads:** <https://github.com/kpatr1981/cronman/releases/latest>
- **Author:** Konstantinos Patronas, kpatronas@gmail.com

---

## What cronman does

cronman has two modes:

```text
cronman [options] [user@]host           # interactive editor for that user's crontab
cronman --audit [options] [user@]host   # check every user's cron jobs (needs root or sudo)
cronman --local [--audit]                # this machine instead of SSH (macOS/Linux)
```

Nothing gets installed on the target server. cronman sends small POSIX `sh` scripts over
your normal SSH connection, so any Unix box with cron and a shell will do: Debian, Ubuntu,
RHEL, Alpine, even the odd BSD.

### For each job, it checks:

- the script or binary **exists**, symlinks aren't broken, and every parent directory can be entered
- the **execute bit**, plus the read bit for scripts (`bash x.sh` and `python x.py` only need read)
- the `#!` **interpreter exists**, and `#!/usr/bin/env node` finds `node` in **cron's** `PATH`,
  not yours
- bare commands (`php`, `node`, `aws`, …) are in cron's `PATH`, and if not, where they actually are
- **Windows CRLF line endings** (the classic `bad interpreter: /bin/bash^M`)
- the file isn't on a filesystem mounted **`noexec`**
- output **redirects** (`>> /var/log/x.log`) and `flock` lock files can be written
- `cd /some/dir && ...` directories exist, and relative paths are flagged (cron starts in `$HOME`)

It looks through the usual wrappers: `nice`, `ionice`, `timeout`, `flock`, `env`, `nohup`,
and `sh -c '…'`. If a path is built from `$(...)` or an unknown variable, cronman says it
couldn't check it rather than guessing.

---

## Installing

### Option 1: download a binary (recommended)

Prebuilt static binaries are on the
[Releases page](https://github.com/kpatr1981/cronman/releases/latest) for Linux, macOS and
Windows, on both x86-64 and ARM64:

| OS | CPU | file |
|---|---|---|
| Linux | x86-64 | `cronman-linux-amd64` |
| Linux | ARM64 (Raspberry Pi 4/5, Graviton) | `cronman-linux-arm64` |
| macOS | Intel | `cronman-darwin-amd64` |
| macOS | Apple Silicon | `cronman-darwin-arm64` |
| Windows | x86-64 | `cronman-windows-amd64.exe` |
| Windows | ARM64 | `cronman-windows-arm64.exe` |

On Linux:

```sh
curl -LO https://github.com/kpatr1981/cronman/releases/latest/download/cronman-linux-amd64
chmod +x cronman-linux-amd64
sudo mv cronman-linux-amd64 /usr/local/bin/cronman
cronman --version
```

To check the download, grab `SHA256SUMS` from the same release and run:

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

(On macOS use `shasum -a 256 -c SHA256SUMS --ignore-missing`. If you downloaded with a
browser, clear the Gatekeeper quarantine with
`xattr -d com.apple.quarantine /usr/local/bin/cronman`.)

### Option 2: build it yourself

cronman is plain Go with no cgo, so you only need [Go 1.26+](https://go.dev/dl/) and `git`:

```sh
git clone https://github.com/kpatr1981/cronman.git
cd cronman
go build -o cronman .
./cronman --version
```

or in one line:

```sh
go install github.com/kpatr1981/cronman@latest
```

The Makefile has a few shortcuts:

```sh
make test                   # go vet + unit tests
make release                # binaries for all 6 platforms + SHA256SUMS in dist/
make release VERSION=1.1.0  # stamp your own version number
```

Cross-compiling for that Raspberry Pi in the closet is one command:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o cronman-linux-arm64 .
```

---

## Logging in: SSH keys or passwords

cronman doesn't implement SSH. It runs your system's `ssh` client. That means **everything
your `ssh` can do, cronman can do**:

- **SSH keys:** your default key, `-i ~/.ssh/id_ed25519`, ssh-agent, hardware keys
- **passwords and key passphrases:** `ssh` asks for them in the terminal as usual
- **`~/.ssh/config`:** aliases, `User`, `Port`, `ProxyJump` all apply, so `cronman web1` just works
- `-p`, `-i`, `-J` and `-o Option=value` go straight through to `ssh`

```sh
cronman alice@web1.example.com
cronman -i ~/.ssh/ops_key -p 2222 alice@web1
cronman -J bastion.example.com alice@10.0.3.17
```

On Linux and macOS cronman opens **one** SSH connection (`ControlMaster`) and reuses it.
If you use a password, you type it once per run, not once per check. Windows' OpenSSH can't
share connections, so there I recommend keys or ssh-agent.

---

## Interactive mode: a crontab editor that checks your work

```sh
cronman alice@web1
```

cronman logs in as `alice` and lists every job in her crontab, active and commented-out.
Next to each job is a status:

- **OK**: everything the job needs is in place for alice
- **WARN**: it will probably run, but something is off (no `#!` line, CRLF endings, …)
- **FAIL**: cron *will* fail to run it (missing file, no execute permission, bad interpreter…)
- **?**: couldn't check it (path built from variables or `$(...)`)

The keys are always shown at the bottom of the screen:

| key | action |
|---|---|
| `↑ ↓` / `j k` | move |
| `space` / `t` | comment out / un-comment the job |
| `e` | edit the schedule (validated, shows the next run times) |
| `c` | edit the whole command |
| `p` | change only the script/executable path |
| `enter` | details: every file the job uses, problems, numbered fixes |
| `1-9` (in details) | apply a proposed fix (asks first) |
| `w` | save |
| `d` / `r` / `R` | diff / reload / re-run checks |
| `?` / `q` | help / quit |

A few details I'm happy with:

- **Schedule editing is validated.** Type `*/15 9-17 * * 1-5` and cronman shows you the next
  run times right away, so you don't wait until Monday to find out you got it wrong.
- **Enabling and disabling is one key.** cronman tells a *disabled job* from a real comment.
  `## 0 3 * * 0 /opt/cleanup.sh` is a job you switched off; `## test this later` and the
  `# m h dom mon dow command` header in Debian/Ubuntu crontabs stay comments.
- **Saving is careful.** `w` shows a diff first. It refuses to save if someone else changed the
  crontab while you were editing, and it backs up the old version to `~/.cronman_backups/`.
  Nothing is written to the server until you press `w` and confirm.
- **Fixes are minimal.** Press `enter` on a failing job and you get a numbered list of fixes.
  cronman picks the least invasive one: `chmod u+x` if alice owns the file, otherwise
  `sudo chmod g+x`/`o+x` or `chown`. Sudo fixes run through `ssh -t`, so you type your sudo
  password in your own terminal.

---

## Audit mode: check every cron job on a server

This is the mode I run first on a server I've just inherited:

```sh
cronman --audit admin@web1
```

With root, passwordless sudo, or a sudo password you type once, cronman reads:

- every user crontab (`/var/spool/cron/crontabs`, `/var/spool/cron`, …)
- `/etc/crontab` and `/etc/cron.d/*`
- `/etc/cron.hourly`, `daily`, `weekly`, `monthly`

and checks each job **as the user it runs as**. It also catches the problems that live
*around* jobs:

- **orphaned crontabs** (the user no longer exists, so cron ignores the file)
- crontabs with the **wrong owner**, or that are group/world-writable
- `cron.d` files and run-parts scripts with a **`.` in the name**. Debian/Ubuntu silently
  skip them, which is why `backup.sh` in `/etc/cron.daily` never runs.
- run-parts scripts without the execute bit
- users blocked by `cron.allow` / `cron.deny`
- the cron daemon not running at all

Here's a typical run (trimmed):

```text
 cronman audit  web1  (reading as root)

== user alice /var/spool/cron/crontabs/alice
   ✗ line 2    30 2 * * *    /home/alice/bin/backup.sh >> /home/alice/logs/backup.log 2>&1
        ✗ /home/alice/bin/backup.sh is not executable by alice (owner alice:alice mode 0644)
            fix: sudo chmod u+x /home/alice/bin/backup.sh  # give the owner (alice) execute permission
   ✗ line 3    0 7 * * 1-5   /home/alice/bin/report.sh
        ✗ the #! line ends with CR (Windows line endings): you will get 'bad interpreter: /bin/bash^M'
            fix: sudo sed -i 's/\r$//' /home/alice/bin/report.sh  # convert Windows (CRLF) line endings to Unix (LF)
   ✗ line 4    */15 * * * *  /home/alice/bin/sync.js
        ✗ #!/usr/bin/env node: 'node' is not in cron's PATH (/usr/bin:/bin)

== user olduser /var/spool/cron/crontabs/olduser
   ✗ user 'olduser' does not exist; cron ignores this crontab (orphan)
       fix: sudo rm /var/spool/cron/crontabs/olduser  # remove the orphaned crontab

Summary: 2 crontab file(s), 6 job(s) (1 disabled, not counted) — 4 failing
Only problems are listed; use -v to list every job.
```

Four broken jobs, and every one would have "worked fine when I ran it". The `node` one is my
favourite: it runs perfectly in an interactive shell, because *your* `PATH` has
`/usr/local/bin`. Cron's doesn't. Fix it with `PATH=/usr/local/bin:/usr/bin:/bin` at the top
of the crontab, or use the full path to the interpreter.

Useful flags:

- `-v` lists every job, not only the problems
- `--fix` walks through the problems and offers to apply each fix, asking every time

### Using it in scripts and monitoring

`--audit` has a script-friendly exit status:

| exit | meaning |
|---|---|
| `0` | no failing jobs |
| `1` | failing jobs found |
| `2` | error (couldn't connect, etc.) |

So a small loop over your fleet makes a nightly report:

```sh
#!/bin/sh
for h in web1 web2 db1 worker1; do
    cronman --audit --no-color "admin@$h" > "/var/tmp/cron-audit-$h.txt" 2>&1
    case $? in
        0) ;;
        1) echo "cron problems on $h — see /var/tmp/cron-audit-$h.txt" ;;
        *) echo "could not audit $h" ;;
    esac
done
```

(Use key-based auth for unattended runs, of course.)

---

## Running it locally

On your own Linux box or Mac, skip SSH:

```sh
cronman --local            # edit your own crontab
sudo cronman --local --audit
```

---

## Wrapping up

Cron is one of the oldest and most reliable tools we have. When a job fails, though, it fails
silently, and you only find out when you need last night's backup. cronman can't stop people
from `scp`-ing CRLF scripts from their laptops, but now you'll know about it before the
backup is missing.

Give it a try, and if it finds something embarrassing on one of your servers (it will),
I'd love to hear about it. Issues and pull requests are welcome on GitHub:

👉 **<https://github.com/kpatr1981/cronman>**

Happy hacking, and keep loving the penguin 🐧

*Konstantinos Patronas, kpatronas@gmail.com*
