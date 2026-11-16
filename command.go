package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"unicode"
)

// NoArgs is the arguments type of a command that declares no flags or
// arguments, of a group without own flags, and of a node with neither bind nor
// run:
//
//	app.Command[cli.NoArgs]("placeholder", nil, nil)
type NoArgs = struct{}

// app is the state shared by all nodes of one application.
type app struct {
	errs    []error
	busy    atomic.Bool
	version string
	helpFn  func(io.Writer, CommandInfo) error
	helpPos string

	help        *flagDef
	versionFlag *flagDef
}

func (a *app) fail(pos, format string, args ...any) {
	a.errs = append(a.errs, &SchemaError{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// Command is a node of the command tree. The root node is the application.
type Command struct {
	app      *app
	parent   *Command
	name     string
	aliases  []string
	desc     string
	hidden   bool
	deprec   string
	flags    []*flagDef
	args     []*argDef
	children []*Command
	wrap     func(context.Context, Next) error
	wrapPos  string
	checks   []func(context.Context) error
	run      func(context.Context) error
	data     any // pointer to the arguments struct
}

// New creates the root of a command tree, which is the application. The
// arguments type T is inferred from bind or run; bind declares the flags that
// apply to the whole application, and run, if not nil, handles a call without
// a subcommand.
func New[T any](name string, bind func(*Builder, *T), run func(context.Context, *T) error) *Command {
	a := &app{
		help:        &flagDef{name: "help", short: 'h', kind: kindHelp, desc: "Show help"},
		versionFlag: &flagDef{name: "version", kind: kindVersion, desc: "Show version"},
	}
	c := &Command{app: a, name: name, data: new(T)}
	if name == "" {
		a.fail(callerPos(1), "application name is empty")
	}
	register(c, bind, run)
	return c
}

// Command registers a subcommand and returns its node. Flags of c apply in the
// whole subtree of c. A node with subcommands takes no positional arguments.
func (c *Command) Command[T any](name string, bind func(*Builder, *T), run func(context.Context, *T) error) *Command {
	pos := callerPos(1)
	n := &Command{app: c.app, parent: c, name: name, data: new(T)}
	attach := true
	switch {
	case !validName(name):
		c.app.fail(pos, "invalid command name %q", name)
		attach = false
	case len(c.args) > 0:
		c.app.fail(pos, "command %q: cannot add a subcommand to %q, which declares positional arguments", name, c.name)
		attach = false
	case c.child(name) != nil:
		c.app.fail(pos, "command %q already declared", name)
		attach = false
	}
	register(n, bind, run)
	if attach {
		c.children = append(c.children, n)
	}
	return n
}

func register[T any](c *Command, bind func(*Builder, *T), run func(context.Context, *T) error) {
	data := c.data.(*T)
	if run != nil {
		c.run = func(ctx context.Context) error { return run(ctx, data) }
	}
	b := &Builder{cmd: c}
	if bind != nil {
		bind(b, data)
	}
	b.seal()
}

// Err returns the schema errors collected while the application was built,
// joined with errors.Join, or nil. Each of them is a *SchemaError.
func (c *Command) Err() error { return errors.Join(c.app.errs...) }

func (c *Command) root() *Command {
	for c.parent != nil {
		c = c.parent
	}
	return c
}

func (c *Command) path() []string {
	var p []string
	for n := c; n != nil; n = n.parent {
		p = append(p, n.name)
	}
	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}
	return p
}

func (c *Command) child(name string) *Command {
	for _, ch := range c.children {
		if ch.name == name {
			return ch
		}
		for _, a := range ch.aliases {
			if a == name {
				return ch
			}
		}
	}
	return nil
}

// resetAll clears every bound field of the tree and its source mark.
func (c *Command) resetAll() {
	for _, d := range c.flags {
		d.reset()
	}
	for _, a := range c.args {
		a.reset()
	}
	for _, ch := range c.children {
		ch.resetAll()
	}
}

// visible reports whether the node is offered in help, suggestions and
// completion.
func (c *Command) visible() bool { return !c.hidden && c.deprec == "" }

func validName(s string) bool {
	if s == "" || s[0] == '-' {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool { return r == '=' || unicode.IsSpace(r) || !unicode.IsPrint(r) })
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
