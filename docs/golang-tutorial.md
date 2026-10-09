# Learn Go by reading cronman

A Go tutorial built around a real program, **cronman** (the code in this repository).
It teaches every language feature, standard-library package and tool that cronman uses,
using the code as the examples, so after working through it you can read every file in this
repo and change it yourself.

Each chapter has:

* **the idea**, explained from scratch;
* **where cronman uses it**, with the file name so you can open it next to the text;
* a small **try it** exercise. Do them. Go is learned by typing it.

No previous Go is assumed. Some programming experience (any language) and basic
shell knowledge are assumed.

---

## Contents

**Part 1: The language**

1. [Setup and your first program](#1-setup-and-your-first-program)
2. [Variables, types and constants](#2-variables-types-and-constants)
3. [Strings, bytes and runes](#3-strings-bytes-and-runes)
4. [Control flow: if, for, switch](#4-control-flow-if-for-switch)
5. [Functions](#5-functions)
6. [Slices](#6-slices)
7. [Maps](#7-maps)
8. [Structs](#8-structs)
9. [Pointers and methods](#9-pointers-and-methods)
10. [Interfaces](#10-interfaces)
11. [Errors](#11-errors)
12. [defer](#12-defer)
13. [Goroutines and channels (the little cronman needs)](#13-goroutines-and-channels)

**Part 2: The standard library cronman uses**

14. [fmt: printing and formatting](#14-fmt)
15. [strings and strings.Builder](#15-strings-and-stringsbuilder)
16. [regexp](#16-regexp)
17. [strconv, sort, unicode/utf8](#17-strconv-sort-unicodeutf8)
18. [time](#18-time)
19. [os, os/exec, bytes: running other programs](#19-os-osexec-bytes)
20. [os/signal: surviving ctrl-c](#20-ossignal)
21. [flag: command-line options](#21-flag)
22. [bufio, crypto/rand, encoding/hex, path/filepath](#22-bufio-cryptorand-encodinghex-pathfilepath)

**Part 3: Building a real project**

23. [Packages, files and visibility](#23-packages-files-and-visibility)
24. [Modules and dependencies (go.mod)](#24-modules-and-dependencies)
25. [Platform-specific code: build constraints](#25-build-constraints)
26. [Testing](#26-testing)
27. [The toolchain: fmt, vet, build, cross-compile, release](#27-the-toolchain)
28. [How cronman fits together](#28-how-cronman-fits-together)
29. [Gotchas cheat sheet](#29-gotchas-cheat-sheet)
30. [Where to go next](#30-where-to-go-next)

---

# Part 1: The language

## 1. Setup and your first program

Install Go from <https://go.dev/dl/> (or `brew install go`), then check it:

```sh
go version        # cronman needs 1.26 or newer (see go.mod)
```

Make a scratch folder and a module (Chapter 24 explains what a module is):

```sh
mkdir hello && cd hello
go mod init example.com/hello
```

Create `main.go`:

```go
package main

import "fmt"

func main() {
	fmt.Println("hello, cron")
}
```

Run it:

```sh
go run .          # compile + run in one step
go build -o hello . && ./hello   # produce a binary
```

What to notice:

* **Every Go file starts with `package <name>`.** An executable program lives in
  `package main` and starts at `func main()`. cronman is entirely `package main`:
  look at the top of any `.go` file in this repo.
* **`import`** pulls in packages. Several go in a parenthesised block, as in `main.go`:

  ```go
  import (
  	"flag"
  	"fmt"
  	"io"
  	"os"
  	"runtime"
  	"strings"
  )
  ```

  An unused import is a **compile error**, not a warning. So is an unused local variable.
  Go is strict so that code stays clean.
* **Formatting is not a matter of taste.** Tabs for indentation, braces on the same line.
  `gofmt` (Chapter 27) does it for you, and every Go project looks the same.
* There are **no semicolons** at line ends (the compiler inserts them). That's why
  `{` must stay on the same line as `if`, `for` and `func`.

**Try it:** put `x := 1` inside `main` without using `x`, and run it. Read the error.

---

## 2. Variables, types and constants

### Declaring variables

```go
var name string = "cron"   // full form
var count int              // no value: gets the "zero value" 0
user := "alice"            // short form: type is inferred; only inside functions
a, b := 1, 2               // several at once
```

**Zero values** are important in Go. Every variable is initialised:
`0` for numbers, `""` for strings, `false` for bools, and `nil` for pointers, slices,
maps, channels, functions and interfaces. There is no "uninitialised" state.

cronman relies on zero values constantly. In `run.go`:

```go
pid, log := "", ""          // explicit, but "var pid, log string" would be identical
```

and in `tui.go` the `app` struct is created with only two fields set. The rest
(`sel`, `off`, `status`, …) start at their zero values:

```go
a := &app{r: r, info: ri}
```

A group of package-level variables can share one `var ( … )` block. `main.go` declares
all the command-line flags that way:

```go
var (
	audit    = flag.Bool("audit", false, "check all users' cron jobs …")
	local    = flag.Bool("local", false, "use this machine instead of SSH (macOS/Linux)")
	port     = flag.String("p", "", "SSH port")
	...
)
```

### Basic types

| type | example | notes |
|---|---|---|
| `bool` | `true` | |
| `int`, `int64`, `uint32`, … | `42` | `int` is 64-bit on modern machines |
| `float64` | `3.14` | |
| `string` | `"text"` | immutable sequence of **bytes** (Chapter 3) |
| `byte` | `'x'` | alias for `uint8` |
| `rune` | `'→'` | alias for `int32`, one Unicode code point |

Go **never converts types implicitly**. `int` + `int64` is a compile error; you write
`int64(x)`. Conversions look like function calls: `float64(n)`, `string(runes)`,
`[]byte(s)`, `Severity(2)`.

### Constants and `iota`

```go
const version = "1.1.0"   // untyped constant
```

`main.go` uses a constant block:

```go
const (
	author  = "Konstantinos Patronas <kpatronas@gmail.com>"
	repoURL = "https://github.com/kpatr1981/cronman"
)
```

(`version` itself is a `var`, not a `const`, so the build can overwrite it. See Chapter 27.)

Go has no `enum` keyword. You make one from a **named type** and **`iota`**, a counter
that starts at 0 and goes up by one per line in a `const` block. From `check.go`:

```go
type Severity int

const (
	SevOK Severity = iota // 0
	SevInfo               // 1  (type and "= iota" are repeated implicitly)
	SevWarn               // 2
	SevFail               // 3
)
```

Because `Severity` is its own type, the compiler won't let you pass a plain
`LineKind` where a `Severity` is expected, even though both are ints underneath.
Since the values are ordered you can compare them: `if s > worst { worst = s }`.

`crontab.go` does the same for the kinds of crontab line:

```go
type LineKind int

const (
	KBlank   LineKind = iota
	KComment          // a real comment (prose, headers, examples)
	KEnv              // NAME=value
	KJob              // a job, active or commented out (Disabled)
	KInvalid          // non-comment line that cron cannot parse
)
```

Constants can also be characters. `shell.go`:

```go
const (
	TExec  = 'x' // must be executable
	TBare  = 'b' // bare command name, looked up in cron's PATH
	TRead  = 'r'
	TWrite = 'w'
	TDir   = 'd'
)
```

**Try it:** declare `type Weekday int` with `Sunday … Saturday` using `iota`, and print
`Wednesday`. You get `3`. Chapter 10 shows how to make it print `"Wednesday"`.

---

## 3. Strings, bytes and runes

A Go `string` is an **immutable sequence of bytes**, normally UTF-8 text.

```go
s := "→ ok"
len(s)        // 6, not 4: '→' takes 3 bytes in UTF-8
s[0]          // a byte (0xE2), not a character
```

* Indexing `s[i]` gives a **byte**. Slicing `s[a:b]` gives a substring (by byte offsets).
* `for i, r := range s` walks **runes** (characters), decoding UTF-8 for you.
* `[]rune(s)` converts to a slice of characters, which you can index by character.

cronman meets this in the terminal UI. Column widths must count characters, not bytes,
so `term.go` has `visibleLen` (it also skips colour escape codes). The line editor keeps
its text as `[]rune` so the cursor moves one character at a time (`tui.go`, `prompt`):

```go
ed := &lineEditor{buf: []rune(initial), pos: len([]rune(initial))}
```

The shell lexer in `shell.go` works byte by byte instead, because all of the shell's
special characters (`$ ' " | ;`) are plain ASCII:

```go
func isNameStart(c byte) bool { return c == '_' || (c|0x20) >= 'a' && (c|0x20) <= 'z' }
```

(`c|0x20` is an old ASCII trick: it lower-cases a letter by setting one bit.)

### Raw strings

Backquotes make a **raw string literal**: no escapes, and newlines are allowed. That's
how cronman embeds whole shell scripts in Go (`tui.go`):

```go
const infoScript = `
printf '@@USER %s\n' "$(id -un)"
printf '@@HOME %s\n' "$HOME"
...
`
```

The `\n` inside is sent to the shell as backslash + n, and `printf` turns it into a
newline. In a `"double-quoted"` Go string, Go itself would have turned `\n` into a newline.

Single quotes are for **one character** (a rune): `'x'`, `'\n'`. They are never strings.

**Try it:** print `len("héllo")` and `len([]rune("héllo"))`. Then loop over it with
`for i, r := range "héllo" { fmt.Println(i, string(r)) }` and look at the indexes.

---

## 4. Control flow: if, for, switch

### if

There are no parentheses, and the braces are always required. An `if` can start with a short
statement, and a variable declared there exists only inside the `if`/`else`:

```go
if d := Describe(l.Schedule); d != "" {
	a.status += " (" + d + ")"
}
// d does not exist here
```

That's from `tui.go`, `editSchedule`. You'll see this pattern everywhere, especially with
errors: `if err := f(); err != nil { … }`.

### for: Go's only loop

```go
for i := 0; i < len(cmd); i++ { … }   // C style      (crontab.go, CronShellPart)
for len(k.pending) == 0 { … }         // "while"      (term.go, keyReader.Read)
for { … }                              // forever      (tui.go, app.loop)
for i, l := range lines { … }         // range over a slice: index, element
for _, l := range lines { … }         // _ discards the index
for k := range env { … }              // range over a map: keys only
for i := range 10 { … }               // 0..9 (Go 1.22+)
```

`break` leaves the loop and `continue` skips to the next iteration. The main loop in
`tui.go` is `for { … }`, and it ends by `return`ing from the function when you quit.

Since Go 1.22, each iteration gets a **fresh copy** of the loop variable, so taking its
address or capturing it in a closure is safe (it was a classic bug in older Go).

### switch

A Go `switch` **does not fall through**: only the matching case runs, so no `break` is
needed. A case can list several values:

```go
switch a.ask(base, q, "y = run …   b = run in background …") {
case 'y', 'Y':
	a.runForeground(l, n)
case 'b', 'B':
	a.runBackground(l, n)
default:
	a.status = "Cancelled."
}
```

(from `run.go`). A **switch without a value** is a tidy `if / else if` chain. Each case is
a boolean, and the first true one wins. The key handler in `tui.go` is written this way:

```go
switch {
case k.Name == "up" || k.Rune == 'k':
	if a.sel > 0 {
		a.sel--
	}
case k.Rune == 'x':
	a.runNow(nil)
case k.Rune == '?' || k.Rune == 'h':
	a.help()
}
```

Inside a `for`, a plain `break` in a `switch` leaves the **switch**, not the loop. Use
`return` or a labelled break (`break loop`) to get out of the loop.

**Try it:** write FizzBuzz using a tagless `switch`.

---

## 5. Functions

```go
func plural(n int, s string) string {
	if n == 1 {
		return "1 " + s
	}
	return fmt.Sprintf("%d %ss", n, s)
}
```

(from `tui.go`). The types come **after** the names. Adjacent parameters of the same type
can share it: `func(a, b int)`.

### Multiple return values

A Go function can return several values, and this is how Go reports errors:

```go
func (r *sshRunner) Exec(remoteCmd, stdin string) (string, string, error)
```

returns stdout, stderr and an error. The caller takes them all, discarding any it
doesn't need with `_`:

```go
out, stderr, err := RunScript(a.r, "", script)
_, current, err := loadRemote(a.r)
```

### Named results

Results can have names. They're then ordinary variables, starting at their zero value.
`CronStdin` in `crontab.go`:

```go
func CronStdin(cmd string) (stdin string, has bool) {
	...
	return s, has
}
```

The names mostly serve as documentation. A bare `return` (with no values) would return
`stdin` and `has` as they are, but outside very short functions it makes code harder to read.

### Variadic functions

`...T` as the last parameter accepts any number of `T`, which arrive as a `[]T`. In
`check.go`, `Analyze` defines a helper that takes zero or more fixes:

```go
add := func(sev Severity, msg string, fixes ...Fix) {
	tr.Issues = append(tr.Issues, Issue{sev, msg, fixes})
}
add(SevInfo, "not checked")                    // fixes is empty
add(SevFail, msg, Fix{…}, Fix{…})              // fixes has 2
```

To pass an existing slice to a variadic function, follow it with `...`:
`append(lines, more...)`.

### Functions are values, and closures

A function can be stored in a variable, passed as an argument and returned. The `add`
above is a **closure**: a function literal that uses (`captures`) the variable `tr` from
the function around it, and changes it.

cronman passes functions around a lot. The `prompt` editor in `tui.go` takes two:

```go
func (a *app) prompt(base func([]string) []string, label, initial string,
	validate func(string) string) (string, bool)
```

* `base` draws whatever screen is behind the prompt. The job list passes `a.render`
  (a **method value**: a method bound to its receiver `a`), and the details screen passes
  its own closure. That's how the same prompt works on both screens.
* `validate` checks the input. `editSchedule` passes a literal:

  ```go
  v, ok := a.prompt(base, "New schedule …", l.Schedule, func(s string) string {
  	if err := ValidateSchedule(strings.Join(strings.Fields(s), " ")); err != nil {
  		return "invalid: " + err.Error()
  	}
  	return ""
  })
  ```

`nil` is a valid function value. `runNow(base)` does `if base == nil { base = a.render }`
so callers can pass `nil` for "the normal screen".

**Try it:** write `func counter() func() int` that returns a closure counting 1, 2, 3, …
on each call.

---

## 6. Slices

A **slice** (`[]T`) is Go's growable list. (Fixed-size **arrays** `[4]int` exist but are
rarely used directly.)

```go
var lines []string                 // nil slice: len 0, usable right away
lines = append(lines, "a", "b")    // append returns the (possibly new) slice
names := make([]string, 0, len(env))  // len 0, room for len(env) before reallocating
buf := make([]byte, 64)            // len 64, all zero
```

* `len(s)` is the length, `cap(s)` the capacity.
* `s[i]` indexes it, and `s[a:b]` is a **sub-slice that shares the same memory**.
* `append` **must** be assigned back: `s = append(s, x)`.

A slice is a small header (pointer, length, capacity) pointing into an underlying array.
That's why sub-slicing is cheap. It's also why two slices can share data, which you
sometimes need to watch for.

### Real uses

A queue of pending bytes in `term.go`. Take the first byte, then drop it by re-slicing:

```go
b := k.pending[0]
k.pending = k.pending[1:]
```

Appending another slice's elements with `...` (also `term.go`):

```go
k.pending = append(k.pending, buf[:n]...)
```

**The aliasing trap, and how cronman avoids it.** `runner.go` builds the ssh arguments like this:

```go
args := append(append([]string{}, r.baseArgs...), "-T", "-o", "LogLevel=ERROR", r.target, remoteCmd)
```

Why not just `append(r.baseArgs, "-T", …)`? If `r.baseArgs` had spare capacity, `append`
would write into **its** underlying array, and two calls could overwrite each other's
arguments. Copying into a fresh `[]string{}` first guarantees a new array. (Go 1.21+ also
has `slices.Clone(r.baseArgs)`.)

**Grow-until-long-enough** (`check.go`):

```go
p := strings.SplitN(s, ":", 3)
for len(p) < 3 {
	p = append(p, "?")
}
```

### Built-in `min` and `max`

Since Go 1.21, `min` and `max` are built in and work with any ordered type:

```go
a.sel = max(0, min(len(a.jobs)-1, a.sel+10))   // clamp, from tui.go (page down)
```

**Try it:** make `s := []int{1,2,3,4}`, then `t := s[:2]`, `t = append(t, 99)`, and print
`s`. Explain the `99`.

---

## 7. Maps

`map[K]V` is a hash table.

```go
env := map[string]string{"PATH": "/usr/bin:/bin", "SHELL": "/bin/sh"}  // literal
env["HOME"] = home                // insert/overwrite
v := env["MISSING"]               // missing key → zero value ("")
v, ok := env["HOME"]              // "comma ok": ok is false if absent
delete(env, "HOME")
len(env)
```

The literal above is exactly how cron's default environment starts in `crontab.go`
(`EnvAt`).

**A nil map reads fine but panics on write.** Always create maps with a literal or
`make(map[K]V)` before inserting. `tui.go`, `setContent`, creates two fresh maps each time
a crontab loads:

```go
a.rep = map[*Line]*JobReport{}
a.ext = map[*Line]*Extraction{}
```

Notice the key type: **`*Line`, a pointer**. Any comparable type can be a key, and pointers
compare by identity. So "the report for *this* line object" works even when two lines have
the same text.

### Sets

Go has no set type. Use `map[T]bool` (or `map[T]struct{}`). `tui.go`, `check`, avoids
asking the host about the same file twice:

```go
idx := map[string]bool{}
for _, t := range ex.Targets {
	rq := reqFor(t)
	if !idx[rq.key()] {       // missing key → false
		idx[rq.key()] = true
		reqs = append(reqs, rq)
	}
}
```

### Iteration order is random

`for k, v := range m` visits keys in an **unspecified, deliberately varying** order. When
order matters, collect the keys and sort them. `run.go` does this so the `env -i`
command line comes out the same every time:

```go
names := make([]string, 0, len(env))
for k := range env {
	names = append(names, k)
}
sort.Strings(names)
for _, k := range names {
	b.WriteString(" " + shq(k+"="+env[k]))
}
```

**Try it:** count word frequencies in a sentence with a `map[string]int`, then print them
sorted by word.

---

## 8. Structs

A struct groups named fields:

```go
type Fix struct {
	Desc     string
	Cmd      string
	Sudo     bool
	EditFrom string
	EditTo   string
}
```

(`check.go`). Creating values:

```go
f := Fix{Desc: "make it executable", Cmd: "chmod u+x /x"}   // keyed: omitted fields = zero
i := Issue{sev, msg, fixes}                                  // positional: every field, in order
p := &Fix{Desc: "x"}                                         // pointer to a new struct
var empty Fix                                                // all fields zero
```

Prefer **keyed** literals. They keep working when fields are added. Positional is fine for
tiny private structs like `Issue` and `ownerInfo{p[0], p[1], p[2]}`.

Access fields with a dot. The dot also works through a pointer (`p.Desc`, no `->`).

### Comments on fields

A trailing comment documents a field. `crontab.go`'s `Line` is a good example of a
well-described struct:

```go
type Line struct {
	Num      int
	Raw      string
	Kind     LineKind
	Disabled bool   // job that is commented out
	Prefix   string // original comment prefix of a disabled job, e.g. "## "
	...
	origDisabled bool   // lower case: private to the package (Chapter 23)
	origPrefix   string
}
```

### Anonymous structs

A struct type can be written inline with no name. Tests use this for tables of cases
(`crontab_test.go`):

```go
cases := []struct {
	line     string
	kind     LineKind
	disabled bool
}{
	{"#0 5 * * * /opt/backup.sh", KJob, true},
	{"# m h  dom mon dow   command", KComment, false},
}
```

### The empty struct

`struct{}` has no fields and takes zero bytes. `runner.go` uses it for a type that needs
no data, only methods:

```go
type localRunner struct{}
```

Structs are **values**: assigning one or passing it to a function copies every field. To
share one struct between several places, use a pointer (next chapter).

**Try it:** define `type Job struct{ Schedule, Command string; Enabled bool }`, make a
slice of three jobs, and print the enabled ones.

---

## 9. Pointers and methods

### Pointers

`&x` takes the address of `x`, and `*p` reads or writes through the pointer `p`. There is
no pointer arithmetic. Go has a garbage collector, so returning a pointer to a local
variable is safe:

```go
func newKeyReader() *keyReader { return &keyReader{} }   // term.go
```

A `newXxx` function like that is Go's **constructor convention**. It's just an ordinary
function. `newSSHRunner` in `runner.go` is a larger one: it builds the SSH argument list,
then returns `r`.

### Methods

A method is a function with a **receiver**, written before its name:

```go
func (o ownerInfo) String() string { … }       // value receiver: gets a copy
func (l *Line) SetDisabled(d bool) { … }       // pointer receiver: can modify l
```

The rule of thumb:

* **Pointer receiver** (`*T`) when the method changes the value, or the struct is big. In
  cronman `*Line`, `*Crontab`, `*app`, `*sshRunner` and `*keyReader` all use pointer receivers
  because they hold state that changes.
* **Value receiver** (`T`) for small, read-only types (`ownerInfo`, `remoteInfo.ctx`,
  `localRunner`).
* Don't mix the two on one type without a reason.

You call both kinds the same way (`l.SetDisabled(true)`), and Go takes `&l` for you
automatically.

`SetDisabled` (`crontab.go`) shows why the pointer matters. It changes fields that the
caller must see afterwards:

```go
func (l *Line) SetDisabled(d bool) {
	if l.Disabled == d {
		return
	}
	l.Disabled = d
	...
	l.Modified = true
}
```

With a value receiver, it would change a copy, and the caller would never see it.

### Methods on any named type

A method can go on any type you define, not only structs. `main.go` defines a slice type
with methods:

```go
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
```

`*m = append(*m, v)` writes the grown slice **back through the pointer**. That's why
`Set` needs a pointer receiver.

### Methods on a nil pointer

A pointer-receiver method can be called on a `nil` pointer, and handle it. `check.go`:

```go
func (jr *JobReport) Sev() Severity {
	if jr == nil {
		return SevInfo
	}
	...
}
```

So `a.rep[l].Sev()` is safe even when there's no report for `l` yet: the map returns a
nil `*JobReport`, and `Sev` handles it.

**Try it:** give your `Job` type a `Toggle()` method. Try it with a value receiver, then a
pointer receiver, and see which one actually toggles.

---

## 10. Interfaces

An interface is a **set of method signatures**. Any type that has those methods
satisfies it **automatically**. There is no `implements` keyword.

cronman's central abstraction, from `runner.go`:

```go
// Runner executes POSIX sh scripts on the target machine.
type Runner interface {
	Exec(remoteCmd, stdin string) (string, string, error)
	Interactive(remoteCmd string) error
	Label() string
	Close()
}
```

Two types satisfy it:

* `*sshRunner` runs everything through `ssh user@host …`.
* `localRunner` runs `/bin/sh -c …` on this machine (`--local`).

The rest of the program (the TUI, the checks, the audit, run-now) only ever holds a
`Runner`, so it doesn't know or care which one it has. `main.go` picks one:

```go
var r Runner
switch {
case *local:
	r = localRunner{}
case len(positional) == 1:
	r = newSSHRunner(positional[0], *port, *identity, *jump, sshOpts)
}
```

This is also what makes the code testable. `run_test.go` runs the real job scripts through
`localRunner{}`, with no SSH server needed.

> Interfaces in Go are usually **small** and defined **by the code that uses them**, not
> by the code that implements them.

### Standard interfaces you meet in cronman

* **`fmt.Stringer`**: `String() string`. Any type with this method prints nicely with
  `fmt`. `ownerInfo` in `check.go` has one, so `fmt.Sprint(oi)` gives
  `"owner root:wheel mode 0755"`.
* **`error`**: `Error() string`. Chapter 11.
* **`io.Writer`**: `Write([]byte) (int, error)`. `os.Stderr`, files, `bytes.Buffer` and
  `strings.Builder` are all writers. `main.go` has `func stderr() io.Writer { return os.Stderr }`
  and `fmt.Fprintf(stderr(), …)` writes to it.
* **`flag.Value`**: `String() string` and `Set(string) error`. `multiFlag` implements it, so
  `flag.Var(&sshOpts, "o", …)` lets `-o` be repeated (Chapter 21).

### Type assertions

Code holding an interface value can ask what's inside:

```go
ee, ok := err.(interface{ ExitCode() int })   // run_test.go
if ok {
	fmt.Println(ee.ExitCode())
}
```

The form `x.(T)` checks whether the interface `x` holds a `T`, or something with T's methods
when T is an interface. Here `T` is an **anonymous interface** written inline: "anything
with an `ExitCode() int` method". With `, ok`, a failed assertion gives `ok == false`;
without `, ok`, it panics.

A **type switch** checks several types at once:

```go
switch v := x.(type) {
case string:
	…
case int:
	…
}
```

### The empty interface

`any` (an alias for `interface{}`) is satisfied by every type. `fmt.Println(args ...any)`
uses it. Use it sparingly, because the compiler can't check anything about it.

**Try it:** define `type Shape interface{ Area() float64 }` with `Rect` and `Circle`
types, and sum the areas of a `[]Shape`.

---

## 11. Errors

Go has **no exceptions**. A function that can fail returns an `error` as its last result,
and the caller checks it right away:

```go
ri, content, err := loadRemote(r)
if err != nil {
	return err
}
```

You will write `if err != nil` a lot. That's by design: every place that can fail is
visible in the code.

`error` is just an interface:

```go
type error interface {
	Error() string
}
```

### Making errors

```go
errors.New("unexpected response from host")
fmt.Errorf("crontab -l failed: %s", strings.TrimSpace(errText))   // tui.go
fmt.Errorf("ssh to %s failed: %s", r.target, msg)                // runner.go
```

**Wrapping** with `%w` keeps the original error inside the new one, so callers can still
inspect it:

```go
return fmt.Errorf("reading crontab: %w", err)
```

cronman mostly uses `%v`/`%s`, because its errors only end up on screen. Use `%w` when a
caller might want to examine the cause.

### Inspecting errors: `errors.Is` and `errors.As`

* `errors.Is(err, os.ErrNotExist)` asks whether `err` is, or wraps, that particular error value.
* `errors.As(err, &target)` asks whether `err` is, or wraps, an error of that **type**, and
  if so stores it in `target`.

`run.go` uses `errors.As` to get a job's exit status. When a process exits non-zero,
`exec.Cmd.Run` returns an `*exec.ExitError`:

```go
code := 0
var ee *exec.ExitError
switch {
case err == nil:
case errors.As(err, &ee):
	code = ee.ExitCode()
default:
	fmt.Printf("\n%s %v\n", red("could not run:"), err)
	code = -1
}
```

`runner.go` does the same check with a type assertion. There, exit code 255 from `ssh`
means the connection itself failed:

```go
if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 255 { … }
```

(`errors.As` is the better choice, because it also finds errors that have been wrapped.)

### panic

`panic` is for **programming bugs** (index out of range, nil map write), not for
expected failures. `regexp.MustCompile` panics on a bad pattern. That's acceptable because
a bad pattern is a bug in the source, and it fails as soon as the program starts
(Chapter 16). Ordinary code returns errors.

**Try it:** write `func parsePort(s string) (int, error)` using `strconv.Atoi` that also
rejects values outside 1–65535 with a helpful `fmt.Errorf` message.

---

## 12. defer

`defer f()` runs `f()` **when the surrounding function returns**, however it returns
(a normal return, an early return, or a panic). Deferred calls run in reverse order.
It's how Go guarantees cleanup.

`tui.go`, `runInteractive`:

```go
scr, err := openScreen()       // terminal into raw mode + alternate screen
if err != nil {
	return err
}
a.scr = scr
defer scr.close()              // ALWAYS restore the terminal, even on early return
...
a.loop()
return nil
```

and `main.go`:

```go
defer r.Close()                // close the SSH connection when main returns
```

Two details:

* **Arguments are evaluated immediately**, when the `defer` line runs; only the call
  happens later.
* **`os.Exit` does not run deferred calls.** That's why `main.go` calls `r.Close()`
  explicitly before `os.Exit(code)` and `fatal(…)`. Otherwise the SSH control
  connection would be left behind.

**Try it:** write a function with three `defer fmt.Println(i)` calls in a loop and
predict the output before running it.

---

## 13. Goroutines and channels

This is Go's most famous feature. cronman uses only a tiny piece of it, on purpose, but
you need the basics.

* **Goroutine:** `go f(x)` starts `f` running *concurrently*. Goroutines are very cheap
  (thousands are normal).
* **Channel:** `ch := make(chan T, size)` is a typed pipe between goroutines. `ch <- v`
  sends, `v := <-ch` receives, and each one waits until the other side is ready (or the
  buffer has room).

```go
results := make(chan string)
for _, host := range hosts {
	go func() {
		results <- check(host)    // each host checked in parallel
	}()
}
for range hosts {
	fmt.Println(<-results)
}
```

`sync.WaitGroup` and `sync.Mutex` (package `sync`) coordinate goroutines that share data.
`select { … }` waits on several channels at once.

### Why cronman avoids background goroutines

The comment on `keyReader` in `term.go` explains it:

```go
// keyReader reads keys synchronously (no background goroutine, so child
// processes such as "ssh -t ... sudo" get the terminal's input untouched).
```

A goroutine sitting in a loop reading the keyboard would **steal keystrokes** from
`sudo`'s password prompt when cronman hands the terminal to a child process. So
cronman reads keys only when it is waiting for one. Concurrency is a tool, and not using it
is sometimes the right design.

### The one channel cronman uses

`run.go` uses a channel to receive OS signals (Chapter 20):

```go
sig := make(chan os.Signal, 1)
signal.Notify(sig, os.Interrupt)
```

**Try it:** run the parallel example above with a fake `check` that sleeps for a random
time and returns the host name. Watch the order change between runs.

---

# Part 2: The standard library cronman uses

## 14. fmt

```go
fmt.Println(a, b)                 // spaces between, newline at end
fmt.Printf("%d jobs\n", n)        // formatted, to stdout
s := fmt.Sprintf("%d %ss", n, s)  // formatted, to a string
fmt.Fprintf(os.Stderr, "...")     // formatted, to any io.Writer
fmt.Sprint(n)                     // value → string (run.go: fmt.Sprint(n) for the job number)
```

The verbs you'll see in cronman:

| verb | meaning | example |
|---|---|---|
| `%s` | string (or `Error()`/`String()`) | `"Run job %s"` |
| `%d` | integer | `"line %d:"` |
| `%v` | default format, anything | `"failed: %v"` with an error |
| `%q` | quoted, escaped string, ideal in test messages | `t.Fatalf("got %q", out)` |
| `%-12s` | left-aligned, padded to 12 | `fmt.Sprintf("%-12s", tr.T.Role)` in `tui.go` |
| `%%` | a literal `%` | |

`%v` on a struct prints its fields, `%+v` adds the field names, and `%T` prints the type.
The last two are great for debugging.

---

## 15. strings and strings.Builder

The `strings` package does most of cronman's text work. These are the functions it uses,
and they're worth learning:

```go
strings.TrimSpace(s)              // strip whitespace both ends
strings.TrimPrefix(s, "@@BACKUP ")   // remove prefix if present
strings.TrimSuffix(content, "\n")
strings.TrimLeft(t, "#")          // strip any of these chars from the left
strings.HasPrefix(t, "#")  /  strings.HasSuffix(s, "\n")
strings.Contains(out, "@@RC 0")
strings.Split(content, "\n")      // → []string
strings.SplitN(s, ":", 3)         // at most 3 parts
strings.Fields(s)                 // split on any run of whitespace
strings.Join(parts, ", ")
strings.ReplaceAll(s, "'", `'\''`)
strings.Repeat("─", w)
strings.LastIndex(rest, "\n@@END ")
strings.ToLower(errText)
```

### Cut and CutPrefix

These newer helpers replace a lot of `Index` arithmetic:

```go
k, v, _ := strings.Cut(l, " ")                     // "@@USER alice" → "@@USER", "alice"
head, rest, ok := strings.Cut(out, "@@CRON\n")     // tui.go, loadRemote
if v, ok := strings.CutPrefix(ln, "@@PID "); ok {  // run.go
	pid = v
}
```

The `@@NAME value` lines are how cronman reads results back from its shell scripts: each
script prints marker lines, and the Go side cuts them apart. It's a simple and robust
protocol.

### Building strings efficiently

Strings are immutable, so `s += x` in a loop copies the whole string every time.
`strings.Builder` appends into a growing buffer instead. `CronShellPart` (`crontab.go`)
scans byte by byte:

```go
var b strings.Builder
for i := 0; i < len(cmd); i++ {
	c := cmd[i]
	if c == '\\' && i+1 < len(cmd) && cmd[i+1] == '%' {
		b.WriteByte('%')
		i++
		continue
	}
	if c == '%' {
		break
	}
	b.WriteByte(c)
}
return b.String()
```

`WriteString`, `WriteByte` and `WriteRune` append, and `String()` returns the result.
`screen.draw` in `term.go` builds a whole frame this way and writes it to the terminal in a
single call, which avoids flicker.

### A real helper: shell quoting

`shq` in `runner.go` quotes a string so the shell treats it as one plain word:

```go
func shq(s string) string {
	if s != "" && strings.Trim(s, "abc…XYZ0123456789_-./:=@+,") == "" {
		return s                                              // only safe chars: leave as is
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"   // wrap in '…', escape '
}
```

The `strings.Trim` trick works like this: if removing every safe character leaves nothing,
the string contains only safe characters. Every value cronman puts into a remote command
goes through `shq`. That's what stops a file name like `x; rm -rf ~` from being run.

---

## 16. regexp

```go
var (
	envRe  = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)
	userRe = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]*\$?$`)
	wsRe   = regexp.MustCompile(`[ \t]+`)
)
```

(`crontab.go`). Patterns:

* are written as **raw strings**, so backslashes need no escaping;
* are compiled **once, at package level**. Compiling is slow; matching is fast;
* use `MustCompile`, which panics on a bad pattern. That's fine for a constant pattern: you
  find out the moment the program starts.

Using them:

```go
m := envRe.FindStringSubmatch(line)   // nil, or [whole, group1, group2]
if m != nil {
	name, value := m[1], m[2]
}
userRe.MatchString(u)                 // bool
wsRe.Split(strings.TrimSpace(s), -1)  // split on runs of spaces/tabs (SetSchedule)
```

Go's regexp (RE2 syntax) guarantees linear-time matching, so it has **no backreferences**.
For simple splitting, `strings.Fields` is faster and clearer than a regexp.

---

## 17. strconv, sort, unicode/utf8

**strconv** converts between strings and numbers:

```go
n, err := strconv.Atoi("42")          // string → int, with error
s := strconv.Itoa(42)                 // int → string
strconv.Quote(s)                      // Go-quoted string
```

cronman's schedule parser (`crontab.go`, `parseFieldValue`) uses `Atoi` to read
`"*/15"`, `"1-5"` and similar values, and returns a friendly error when it fails.

**sort** sorts slices: `sort.Strings(names)`, `sort.Ints(xs)`, and for custom orders:

```go
sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })
```

(The newer package `slices` has `slices.Sort` and `slices.SortFunc`.)

**unicode/utf8** handles UTF-8 by hand. `term.go` decodes keyboard input that arrives as
raw bytes:

```go
for !utf8.FullRune(buf) && len(buf) < 4 { … read one more byte … }
r, _ := utf8.DecodeRune(buf)
```

---

## 18. time

```go
now := time.Now()
time.Sleep(20 * time.Millisecond)
elapsed := time.Since(start).Round(100 * time.Millisecond)   // run.go → "3.2s"
t.Add(time.Minute); t.Format("15:04"); t.YearDay()
```

`time.Duration` is an `int64` count of nanoseconds, and it prints nicely (`1.5s`, `2m0s`).

### Layouts

Go formats and parses times with a **reference time** instead of `%Y-%m-%d` codes.
The reference is **Mon Jan 2 15:04:05 MST 2006**, which is 1-2-3-4-5-6-7 in US order
(month 1, day 2, hour 3 PM, minute 4, second 5, year 2006, zone −7). You write how *that*
moment would look:

```go
time.Parse("2006-01-02 15:04:05 -0700", v)   // tui.go: reads the host's clock
t.Format("Mon 01-02 15:04")                    // tui.go: "Thu 10-08 14:07"
```

`schedule.go` computes a job's next run times by stepping a `time.Time` forward and
checking each cron field. Reading it is a good exercise in `time` arithmetic.

---

## 19. os, os/exec, bytes

### os

```go
os.Args                 // command line, os.Args[0] is the program
os.Exit(2)              // quit now with a status (skips defers!)
os.Stdin, os.Stdout, os.Stderr   // *os.File, which are io.Reader/io.Writer
os.Getenv("HOME")
os.ReadFile(path)       // whole file → []byte (run_test.go)
```

### os/exec: running programs

This is the heart of cronman, since everything happens by running `ssh` or `sh`.
From `runner.go`:

```go
func (r *sshRunner) Exec(remoteCmd, stdin string) (string, string, error) {
	args := append(append([]string{}, r.baseArgs...), "-T", "-o", "LogLevel=ERROR", r.target, remoteCmd)
	cmd := exec.Command("ssh", args...)       // program + arguments: NO shell involved
	cmd.Stdin = strings.NewReader(stdin)      // feed a string as stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb      // capture output in memory
	err := cmd.Run()                          // start and wait
	return out.String(), errb.String(), err
}
```

Key points:

* `exec.Command(name, args...)` does **not** go through a shell. Arguments are passed as
  they are, so there's no quoting problem on the local side.
* `Stdin`/`Stdout`/`Stderr` accept any `io.Reader`/`io.Writer`. `strings.NewReader`
  turns a string into a reader, and `bytes.Buffer` collects output.
* `Run` = `Start` + `Wait`. If the process exits non-zero, the error is an
  `*exec.ExitError` (Chapter 11).
* `cmd.Output()` is a shortcut when you only need stdout.

**Handing over the terminal.** `Interactive` connects the child directly to your
terminal, which is how `sudo` can ask for a password and how run-now streams job output live:

```go
cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
return cmd.Run()
```

### The script-over-stdin pattern

cronman never copies files to the host. It **pipes a shell script into `sh -s`**:

```go
func RunScript(r Runner, prefix, script string) (string, string, error) {
	return r.Exec(strings.TrimSpace(prefix+" /bin/sh -s"), script)
}
```

Data goes into that script as a **here-document** with a random end marker that can't
appear in the data (`heredoc` in `runner.go`). That's how a crontab is written back and
how run-now feeds `%` text to a job.

---

## 20. os/signal

Pressing ctrl-c sends **SIGINT** to every process in the terminal's foreground group,
which includes cronman. Normally that kills the program. During run-now the job should
die, but cronman should not. `run.go`:

```go
sig := make(chan os.Signal, 1)
signal.Notify(sig, os.Interrupt)   // deliver SIGINT to this channel instead of dying
start := time.Now()
err := a.r.Interactive("sh -c " + shq(a.foregroundScript(l)))
...
signal.Stop(sig)                   // back to normal behaviour
```

Nobody ever reads from `sig`. Asking for the signal to be delivered is enough to stop the
default "exit" action. The child process still gets its own SIGINT and stops. (Signal handlers
are reset to their defaults in a newly executed program, so the child dies normally.)

---

## 21. flag

The `flag` package parses `-name value` options. From `main.go`:

```go
port    := flag.String("p", "", "SSH port")          // returns *string
verbose := flag.Bool("v", false, "audit: list every job, not only problems")
var sshOpts multiFlag
flag.Var(&sshOpts, "o", "extra SSH option … (repeatable)")   // custom type (Chapter 10)
flag.Usage = usage        // your own help text for -h
flag.Parse()
... *port ...             // dereference to read the value
flag.Args()               // what's left: the positional arguments
```

The constructors return **pointers**, because the value is only filled in when `Parse` runs.

By default `flag` stops at the first non-flag argument, so `cronman host --audit` would
not see `--audit`. `main.go` works around that by parsing repeatedly and collecting the
positional arguments between the flags:

```go
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
```

Go's `flag` accepts both `-audit` and `--audit`.

---

## 22. bufio, crypto/rand, encoding/hex, path/filepath

**bufio**: buffered reading. `term.go` reads a whole line from stdin (used for "Press
Enter to return…"):

```go
var stdinReader = bufio.NewReader(os.Stdin)
s, err := stdinReader.ReadString('\n')
```

For reading a file line by line, `bufio.Scanner` is the usual tool.

**crypto/rand + encoding/hex**: random identifiers. `runner.go`:

```go
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)                 // _ , _ = deliberately ignore both results
	return hex.EncodeToString(b)
}
```

cronman uses it for SSH socket names and here-doc markers. `crypto/rand` is the secure
source; `math/rand` is for simulations.

**path vs path/filepath**: `path` handles slash-separated paths (cronman uses it for paths
**on the remote Unix host**, e.g. `path.Dir(e)` in `check.go`). `path/filepath` uses the
**local** OS's separator (cronman's tests use `filepath.Join`/`filepath.Dir`). The
distinction matters because cronman runs on Windows too, while still talking about Unix
paths.

---

# Part 3: Building a real project

## 23. Packages, files and visibility

* A **package** is a directory of `.go` files that all start with the same `package` line.
  They're compiled together, as if they were one file. cronman's 16 `.go` files are one package:
  `run.go` calls `CronStdin` from `crontab.go` and `shq` from `runner.go` with no imports.
* How the files are split up is for people reading the code; the compiler doesn't care.
  cronman has one file per subject: `crontab.go` (parsing), `shell.go` (lexing commands),
  `check.go` (remote checks), `tui.go` (screen), `run.go` (run-now), and so on.
* **Visibility is set by capitalisation.** `ParseCrontab`, `Line` and `Runner` start with
  a capital letter and are **exported** (visible to other packages). `shq`,
  `origPrefix` and `app` are **unexported** (package-private). Inside `package main` this
  barely matters, but the habit carries over to libraries: capitalise only the API.
* Importing a package: `import "golang.org/x/term"`, then `term.MakeRaw(…)`. The name you
  use is the last part of the path.
* Comments right above a declaration are its **doc comment** (`go doc`, pkg.go.dev). Start
  them with the name: `// Runner executes POSIX sh scripts on the target machine.`

When a program grows, you'd move parts into sub-packages (`internal/crontab`,
`internal/ssh`), each in its own directory. cronman is small enough to live in one.

---

## 24. Modules and dependencies

A **module** is a versioned collection of packages, defined by `go.mod`. cronman's:

```
module github.com/kpatr1981/cronman

go 1.26.0

require (
	golang.org/x/sys v0.48.0
	golang.org/x/term v0.46.0
)
```

* `module` gives the import path. It matches the GitHub URL, so anyone can run
  `go install github.com/kpatr1981/cronman@latest`.
* `go` is the minimum language version.
* `require` lists dependencies. cronman has one direct dependency, `golang.org/x/term`
  (raw terminal mode, window size); `x/sys` comes with it.
* `go.sum` holds cryptographic hashes of every dependency, so builds can be reproduced and
  tampering is detected. Commit it.

Commands:

```sh
go mod init github.com/you/thing   # start a module
go get golang.org/x/term@latest    # add/upgrade a dependency
go mod tidy                        # add missing / remove unused requirements
go list -m all                     # show the full dependency graph
```

Go's standard library is large, so many programs need few dependencies or none.
cronman does SSH, regexps, process control and its whole terminal UI with **one** external
package. That's typical of Go, and it's worth aiming for.

---

## 25. Build constraints

Some code only works on certain systems. Windows' console needs a special call to
understand colour escape codes, and that call doesn't exist on Unix. Go solves this
with **build constraints**: a `//go:build` line at the very top of a file.

`term_unix.go`:

```go
//go:build !windows

package main

func enableVT() bool { return true }
```

`term_windows.go`:

```go
//go:build windows

package main

import (
	"os"
	"golang.org/x/sys/windows"
)

// enableVT turns on ANSI escape processing in the Windows console.
func enableVT() bool {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {   // &mode: pass a pointer to be filled
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
```

Both files define `enableVT`, and exactly one is compiled for any target, so the rest of the
code calls `enableVT()` without caring. A filename ending in `_windows.go`, `_linux.go` or
`_arm64.go` acts as a constraint even without the comment.

For checks at **run time**, use `runtime.GOOS`. `main.go` refuses `--local` on Windows:

```go
if runtime.GOOS == "windows" {
	fatal("--local is not available on Windows (there is no cron); connect to a host instead")
}
```

and `runner.go` skips SSH connection sharing there.

---

## 26. Testing

Testing is built into the language tools. No framework is needed.

* Test files end in **`_test.go`** and are left out of normal builds.
* A test is `func TestXxx(t *testing.T)`.
* Run them with `go test ./...` (`./...` means "this directory and everything below it").

The simplest kind, from `crontab_test.go`:

```go
func TestCronStdin(t *testing.T) {
	if _, has := CronStdin(`date +\%Y >> /tmp/x`); has {
		t.Fatal("no stdin part expected")
	}
	got, has := CronStdin(`mail -s hi root%line one%line \%two`)
	if !has || got != "line one\nline %two\n" {
		t.Fatalf("got %q %v", got, has)
	}
}
```

* `t.Fatal`/`t.Fatalf` fail the test and stop it.
* `t.Error`/`t.Errorf` fail it but keep going, which is useful for reporting several problems at once.
* Print with **`%q`** in failure messages so invisible characters (`\n`, `\t`) show up.

### Table-driven tests

This is the most common Go testing pattern: a slice of cases and one loop.
`TestClassifyCommentedLines` checks 30+ crontab lines this way:

```go
cases := []struct {
	line     string
	kind     LineKind
	disabled bool
}{
	{"## 0 5 * * * /opt/backup.sh", KJob, true},
	{"## backup runs at 5 every day", KComment, false},
	...
}
for _, c := range cases {
	l := parseLine(c.line, false)
	if l.Kind != c.kind || l.Disabled != c.disabled {
		t.Errorf("%q: got kind=%v disabled=%v, want %v %v", c.line, l.Kind, l.Disabled, c.kind, c.disabled)
	}
}
```

To add a case you add one line. `t.Run(name, func(t *testing.T){…})` makes each case a
named sub-test that can be run on its own.

### Helpers used in `run_test.go`

These tests run real generated shell scripts:

```go
func testApp(t *testing.T, crontab string) *app {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")          // skip, don't fail, where it can't run
	}
	home := t.TempDir()                  // fresh dir, deleted automatically afterwards
	...
}

t.Setenv("CRONMAN_LEAK", "leaked")      // set an env var for this test only
```

`TestForegroundScript` sets `CRONMAN_LEAK` in the test's own environment and checks that the
job **does not** see it. That's how it proves the job really gets cron's cleared environment.

Running a subset:

```sh
go test -run 'Foreground|CronStdin' -v ./...   # regexp on test names, verbose
go test -count=1 ./...                         # ignore the cached result
go test -cover ./...                           # coverage percentage
```

Because tests are in the same package, they can call unexported functions (`parseLine`,
`shq`) directly.

---

## 27. The toolchain

```sh
gofmt -l .          # list files that aren't formatted (gofmt -w . fixes them)
go vet ./...        # finds suspicious code: wrong Printf verbs, unreachable code, …
go test ./...
go build -o cronman .
go run . --local    # build + run in one go
go doc strings.Cut  # documentation in the terminal
```

Set up your editor (VS Code + the Go extension, or GoLand) to run `gofmt` on save.
Its language server, `gopls`, gives completion, jump-to-definition and renaming.

### Setting a variable at build time

`main.go` declares `var version = "1.1.0"`. The `Makefile` overwrites it at build time:

```make
go build -ldflags="-X main.version=$(VERSION)" -o cronman .
```

`-X package.variable=value` only works on string **variables**, which is why `version`
isn't a `const`.

### Cross-compiling

Go builds for any OS/CPU from any machine. Set two environment variables:

```sh
GOOS=linux   GOARCH=amd64 go build -o cronman-linux-amd64 .
GOOS=windows GOARCH=arm64 go build -o cronman-windows-arm64.exe .
```

`make release` does this for six targets in a shell loop:

```make
CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o dist/cronman-$$os-$$arch$$ext .
```

* `CGO_ENABLED=0` means no C code, so the result is a **fully static binary** that runs on any
  Linux without needing particular libraries.
* `-trimpath` removes your local file paths from the binary.
* `-ldflags "-s -w"` strips debug symbols, so the file is smaller.

Then `shasum -a 256` writes `SHA256SUMS`, and `gh release create` uploads it all. That's
how the binaries on the GitHub releases page are made.

---

## 28. How cronman fits together

With the language covered, here is the whole program. Read the files in this order.

```
main.go      parse flags → choose a Runner (ssh or local) → audit mode or interactive mode
runner.go    Runner interface; sshRunner / localRunner; shq quoting; heredoc helper
crontab.go   parse crontab text into []*Line; validate schedules; render back byte-for-byte
schedule.go  describe "*/15 * * * *" in words; compute the next run times
shell.go     a small shell lexer: find which files a cron command needs
check.go     one remote shell script checks every file at once; Analyze → issues + fixes
tui.go       the interactive screen: list, details, prompts, save with diff + backup
run.go       run-now: build a cron-like env -i command; foreground or nohup background
audit.go     --audit: read every user's crontab (+ /etc/cron.d) via sudo; report
term*.go     colours, raw terminal mode, key decoding, screen drawing
*_test.go    tests
```

**Following one key press: `x` then `y` on a job**

1. `app.loop` (`tui.go`) waits in `a.scr.keys.Read()` and gets `Key{Rune: 'x'}`.
2. The tagless `switch` matches `case k.Rune == 'x':` and calls `a.runNow(nil)`.
3. `runNow` (`run.go`) shows the command with `a.ask(…)` and gets `'y'`.
4. `runForeground` calls `a.scr.suspend()` (cooked terminal mode, normal screen), starts
   catching SIGINT, and runs `a.r.Interactive("sh -c " + shq(script))`.
5. `script` comes from `foregroundScript`, which uses `jobInvocation`. That builds
   `env -i HOME=… LOGNAME=… PATH=… SHELL=… /bin/sh -c '<command>' </dev/null` from
   `a.ct.EnvAt(l, home)` (`crontab.go`), `CronShellPart` and `CronStdin`, with every value
   passed through `shq`.
6. `Interactive` is either `sshRunner.Interactive` (`ssh -t host 'sh -c …'`) or
   `localRunner.Interactive`. The `Runner` interface hides which.
7. Output streams straight to the terminal. When it finishes, `errors.As(err, &ee)` gets the
   exit code, `time.Since` gives the duration, and `a.scr.resume()` returns to the TUI.

Everything in that path appears earlier in this tutorial: switch, closures, interfaces,
`os/exec`, `os/signal`, `strings.Builder`, maps sorted by key, errors.

**Exercises that change cronman itself** (make a branch first: `git switch -c learning`):

1. *Easy:* add a key `y` to the job list that copies nothing but puts the job's command
   into the status line. You'll touch `mainKeys`, the `switch` in `loop` and `help()`.
2. *Easy:* add a test case to `TestClassifyCommentedLines` for a line you think is tricky.
3. *Medium:* run-now's background mode prints a PID. Add a key that shows the last 20
   lines of the newest log in `~/.cronman_runs/` (hint: `RunScript` with `ls -t | head -1`
   and `tail -n 20`).
4. *Medium:* add a `--timeout` flag: when set, run-now wraps the job in `timeout <secs>` if
   the host has it.
5. *Harder:* add an "add new job" key that asks for a schedule and command (reuse
   `a.prompt` and `ValidateSchedule`) and appends a `*Line` to `a.ct.Lines`.

After each change: `gofmt -l .`, `go vet ./...`, `go test ./...`, then try it for real
with `go run . --local`.

---

## 29. Gotchas cheat sheet

| gotcha | what happens | fix |
|---|---|---|
| unused variable or import | compile error | delete it, or `_ = x` while experimenting |
| `s = append(…)` forgotten | slice unchanged | always assign the result of `append` |
| writing to a nil map | panic | `m := map[K]V{}` or `make(…)` first |
| map iteration order | random order | sort the keys |
| value receiver on a mutating method | changes vanish | use `func (x *T)` |
| `len("é")` is 2 | bytes, not characters | `utf8.RuneCountInString`, `[]rune` |
| `os.Exit` in a function with `defer` | deferred calls never run | clean up before exiting |
| ignoring `err` | silent failures | `if err != nil { return … }` |
| shadowing with `:=` in an inner block | the outer variable isn't updated | use `=` when the variable already exists |
| sub-slice + append | overwrites the original's data | copy first (`append([]T{}, s...)`) |
| `break` inside `switch` inside `for` | only leaves the switch | `return`, or a labelled `break` |
| a nil `*T` stored in an interface | `iface != nil` is **true** | return a literal `nil` for "no error" |

---

## 30. Where to go next

* **A Tour of Go**: <https://go.dev/tour/>. Interactive and in the browser; do it alongside this tutorial.
* **Go by Example**: <https://gobyexample.com>. Short annotated examples for each topic.
* **Effective Go** and **Go Code Review Comments** (on go.dev). How idiomatic Go is written.
* **The standard library docs**: <https://pkg.go.dev/std>. Read the docs for `strings`,
  `os/exec` and `time`; they're short and very good.
* **"The Go Programming Language"** (Donovan & Kernighan). The classic book.
* **Next topics** cronman doesn't use: generics (`func Map[T, U any](…)`),
  `context` for cancellation, `net/http` servers, `encoding/json`, and real concurrency
  with `sync` and `errgroup`.

Good next project ideas that build on what you now know: a `cronman`-style tool for
`systemd` timers, a small HTTP API that serves `cronman --audit` results as JSON, or a
concurrent audit that checks many hosts in parallel with goroutines.
