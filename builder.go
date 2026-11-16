package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"unicode"
)

// Builder declares the schema of one command inside its bind function. It
// must not be used after bind returns: the schema is fixed from then on, and
// later changes are reported as schema errors.
type Builder struct {
	cmd     *Command
	sealed  bool
	flags   []*flagDef
	args    []*argDef
	aliases []declared
}

type declared struct{ name, pos string }

// Next continues a run from inside a wrapper: it calls the nested wrappers
// and finally the run function of the selected command.
type Next func(ctx context.Context) error

// open reports whether declarations may still change. A late change is
// recorded as a schema error at the caller of the public method.
func (b *Builder) open() bool {
	if !b.sealed {
		return true
	}
	b.cmd.app.fail(callerPos(2), "command %q: declaration changed after bind returned", b.cmd.name)
	return false
}

// accept is open for methods that take a function: a nil function is a
// declaration mistake and is reported at the caller, like a nil function
// passed to Check or Wrap of the builder.
func (b *Builder) accept(isNil bool, subject, method string) bool {
	switch {
	case b.sealed:
		b.cmd.app.fail(callerPos(2), "command %q: declaration changed after bind returned", b.cmd.name)
		return false
	case isNil:
		b.cmd.app.fail(callerPos(2), "%s: %s called with a nil function", subject, method)
		return false
	}
	return true
}

// Describe sets the description shown in help. The first line is used in
// command lists.
func (b *Builder) Describe(text string) {
	if b.open() {
		b.cmd.desc = text
	}
}

// Alias adds alternative names for the command.
func (b *Builder) Alias(names ...string) {
	if !b.open() {
		return
	}
	pos := callerPos(1)
	if b.cmd.parent == nil {
		b.cmd.app.fail(pos, "the application cannot have aliases")
		return
	}
	for _, n := range names {
		b.aliases = append(b.aliases, declared{n, pos})
	}
}

// Hidden hides the command from help, typo suggestions and completion. It can
// still be called by name.
func (b *Builder) Hidden() {
	if b.open() {
		b.cmd.hidden = true
	}
}

// Deprecated marks the command as deprecated. It keeps working; using it
// prints a warning with the message to stderr. It is hidden from command
// lists and completion.
func (b *Builder) Deprecated(message string) {
	if !b.open() {
		return
	}
	if message == "" {
		b.cmd.app.fail(callerPos(1), "command %q: deprecation message is empty", b.cmd.name)
		return
	}
	b.cmd.deprec = message
}

// Version declares the application version and enables the built-in
// --version flag. It may only be called for the application itself.
func (b *Builder) Version(v string) {
	if !b.open() {
		return
	}
	switch {
	case b.cmd.parent != nil:
		b.cmd.app.fail(callerPos(1), "command %q: only the application can declare a version", b.cmd.name)
	case v == "":
		b.cmd.app.fail(callerPos(1), "version is empty")
	default:
		b.cmd.app.version = v
	}
}

// HelpFunc replaces the help renderer of the whole application. fn receives
// the command whose help was requested and writes the help to w, which is the
// stdout of the run. It may only be called for the application itself.
func (b *Builder) HelpFunc(fn func(w io.Writer, cmd CommandInfo) error) {
	if !b.open() {
		return
	}
	pos := callerPos(1)
	switch {
	case b.cmd.parent != nil:
		b.cmd.app.fail(pos, "command %q: only the application can replace the help renderer", b.cmd.name)
	case fn == nil:
		b.cmd.app.fail(pos, "help renderer is nil")
	case b.cmd.app.helpFn != nil:
		b.cmd.app.fail(pos, "help renderer already declared at %s", b.cmd.app.helpPos)
	default:
		b.cmd.app.helpFn, b.cmd.app.helpPos = fn, pos
	}
}

// Wrap declares the wrapper of the command. It applies to the whole subtree:
// wrappers nest from the application down to the selected command, and the
// innermost one calls the run function through next. A wrapper that does not
// call next silently cancels the run. At most one wrapper per command.
func (b *Builder) Wrap(fn func(ctx context.Context, next Next) error) {
	if !b.open() {
		return
	}
	pos := callerPos(1)
	switch {
	case fn == nil:
		b.cmd.app.fail(pos, "command %q: wrapper is nil", b.cmd.name)
	case b.cmd.wrap != nil:
		b.cmd.app.fail(pos, "command %q: wrapper already declared at %s", b.cmd.name, b.cmd.wrapPos)
	default:
		b.cmd.wrap, b.cmd.wrapPos = fn, pos
	}
}

// Check adds a check of the whole arguments struct, which fn sees through its
// closure. Checks run after the checks of single values, from the application
// down to the selected command, and also when values are not set. An error is
// returned to the caller as an *InputError. SourceOf works with ctx.
func (b *Builder) Check(fn func(ctx context.Context) error) {
	if !b.open() {
		return
	}
	if fn == nil {
		b.cmd.app.fail(callerPos(1), "command %q: check is nil", b.cmd.name)
		return
	}
	b.cmd.checks = append(b.cmd.checks, fn)
}

// seal validates the declarations collected by bind and adds the valid ones
// to the command. Invalid declarations are reported and skipped, so they do
// not cause further errors.
func (b *Builder) seal() {
	b.sealed = true
	c := b.cmd
	reg := c.ancestorNames()
	for _, d := range b.flags {
		var problems []string
		if !validName(d.name) {
			problems = append(problems, fmt.Sprintf("invalid flag name %q", d.name))
		} else {
			if d.short != 0 && !unicode.IsLetter(d.short) && !unicode.IsDigit(d.short) {
				problems = append(problems, fmt.Sprintf("flag --%s: invalid short name %q", d.name, d.short))
			}
			for _, p := range d.check() {
				problems = append(problems, "flag --"+d.name+": "+p)
			}
		}
		if len(problems) == 0 {
			if msg := reg.claim(d); msg != "" {
				problems = append(problems, msg)
			}
		}
		for _, p := range problems {
			c.app.fail(d.pos, "%s", p)
		}
		if len(problems) == 0 {
			c.flags = append(c.flags, d)
		}
	}

	var variadic, optional *argDef
	for _, a := range b.args {
		var problems []string
		switch {
		case !validName(a.name):
			problems = append(problems, fmt.Sprintf("invalid argument name %q", a.name))
		case slices.ContainsFunc(c.args, func(x *argDef) bool { return x.name == a.name }):
			problems = append(problems, fmt.Sprintf("argument %s already declared", a.name))
		case variadic != nil:
			problems = append(problems, fmt.Sprintf("argument %s declared after variadic argument %s", a.name, variadic.name))
		case a.required() && optional != nil:
			problems = append(problems, fmt.Sprintf("required argument %s declared after optional argument %s", a.name, optional.name))
		}
		if len(problems) == 0 {
			for _, p := range a.check() {
				problems = append(problems, "argument "+a.name+": "+p)
			}
		}
		for _, p := range problems {
			c.app.fail(a.pos, "%s", p)
		}
		if len(problems) > 0 {
			continue
		}
		c.args = append(c.args, a)
		if a.variadic {
			variadic = a
		}
		if !a.required() {
			optional = a
		}
	}

	for _, al := range b.aliases {
		switch {
		case !validName(al.name):
			c.app.fail(al.pos, "invalid alias %q", al.name)
		case al.name == c.name || slices.Contains(c.aliases, al.name):
			c.app.fail(al.pos, "command %q: alias %q already declared", c.name, al.name)
		case c.parent != nil && c.parent.child(al.name) != nil:
			c.app.fail(al.pos, "command %q: alias %q conflicts with command %q", c.name, al.name, c.parent.child(al.name).name)
		default:
			c.aliases = append(c.aliases, al.name)
		}
	}
}

// nameEntry describes who holds a flag name on the path to a command.
type nameEntry struct{ what, pos string }

// names is the set of flag names already taken on the path to a command.
type names struct {
	long  map[string]nameEntry
	short map[rune]nameEntry
}

// ancestorNames collects the flag names of all ancestors of c and the
// built-in flags. Flags of one path must not share long names, short names or
// generated negations.
func (c *Command) ancestorNames() *names {
	n := &names{long: map[string]nameEntry{}, short: map[rune]nameEntry{}}
	n.long["help"] = nameEntry{what: "built-in flag --help"}
	n.short['h'] = nameEntry{what: "built-in short flag -h"}
	if c.app.version != "" {
		n.long["version"] = nameEntry{what: "built-in flag --version"}
	}
	for a := c.parent; a != nil; a = a.parent {
		for _, d := range a.flags {
			n.add(d)
		}
	}
	return n
}

func (n *names) add(d *flagDef) {
	n.long[d.name] = nameEntry{"flag --" + d.name, d.pos}
	if d.negatable {
		n.long["no-"+d.name] = nameEntry{fmt.Sprintf("flag --no-%s (negation of --%s)", d.name, d.name), d.pos}
	}
	if d.short != 0 {
		n.short[d.short] = nameEntry{fmt.Sprintf("short flag -%c of --%s", d.short, d.name), d.pos}
	}
}

// claim takes the names of d, or describes the first conflict.
func (n *names) claim(d *flagDef) string {
	conflict := func(what string, e nameEntry) string {
		if what == e.what && e.pos != "" {
			return what + " already declared at " + e.pos
		}
		if e.pos == "" {
			return what + " conflicts with " + e.what
		}
		return what + " conflicts with " + e.what + " declared at " + e.pos
	}
	if e, ok := n.long[d.name]; ok {
		return conflict("flag --"+d.name, e)
	}
	if d.negatable {
		if e, ok := n.long["no-"+d.name]; ok {
			return conflict(fmt.Sprintf("flag --no-%s (negation of --%s)", d.name, d.name), e)
		}
	}
	if d.short != 0 {
		if e, ok := n.short[d.short]; ok {
			return conflict(fmt.Sprintf("short flag -%c of --%s", d.short, d.name), e)
		}
	}
	n.add(d)
	return ""
}
