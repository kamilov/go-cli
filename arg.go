package cli

import "fmt"

// argDef is the type-independent part of a positional argument declaration.
type argDef struct {
	name     string
	desc     string
	pos      string
	variadic bool
	optional bool
	ptr      any
	defText  string
	hasDef   bool
	complete CompleteFunc
	source   Source

	reset        func()
	set          func(raw string) error
	applyDefault func()
	validate     func() error
	check        func() []string
}

func (a *argDef) required() bool { return !a.optional && !a.hasDef }

func (b *Builder) addArg(a *argDef) {
	if b.sealed {
		b.cmd.app.fail(a.pos, "command %q: argument %s declared after bind returned", b.cmd.name, a.name)
		return
	}
	b.args = append(b.args, a)
}

// Arg describes a positional argument. Its value comes from the command line
// or from the default; there is no environment variable for it.
type Arg[T any] struct {
	b      *Builder
	a      *argDef
	parse  func(string) (T, error)
	def    T
	hasDef bool
	checks []func(T) error
}

// Arg declares the next positional argument, bound to the field p. It is
// required unless Default or Optional is set; a required argument cannot
// follow an optional one. The parser is chosen as for Flag.
func (b *Builder) Arg[T any](p *T, name string) *Arg[T] {
	g := &Arg[T]{b: b}
	g.parse, _ = builtinParser(p)
	a := &argDef{name: name, pos: callerPos(1), ptr: p}
	g.a = a
	a.reset = func() {
		if p != nil {
			var zero T
			*p = zero
		}
		a.source = SourceUnset
	}
	a.set = func(raw string) error {
		v, err := g.parse(raw)
		if err == nil {
			*p = v
		}
		return err
	}
	a.applyDefault = func() { *p = g.def }
	a.validate = func() error { return g.validate(*p) }
	a.check = func() []string {
		switch {
		case p == nil:
			return []string{"field pointer is nil"}
		case g.parse == nil:
			return []string{noParser(p, "Parse", "Args")}
		}
		if g.hasDef {
			a.hasDef, a.defText = true, fmt.Sprint(g.def)
			if err := g.validate(g.def); err != nil {
				return []string{"invalid default value: " + err.Error()}
			}
		}
		return nil
	}
	b.addArg(a)
	return g
}

func (g *Arg[T]) validate(v T) error {
	for _, c := range g.checks {
		if err := c(v); err != nil {
			return err
		}
	}
	return nil
}

// Describe sets the text shown in help.
func (g *Arg[T]) Describe(text string) *Arg[T] {
	if g.b.open() {
		g.a.desc = text
	}
	return g
}

// Default makes the argument optional and sets the value used when it is
// missing.
func (g *Arg[T]) Default(v T) *Arg[T] {
	if g.b.open() {
		g.def, g.hasDef = v, true
		g.a.hasDef = true
	}
	return g
}

// Optional makes the argument optional; when missing it stays unset.
func (g *Arg[T]) Optional() *Arg[T] {
	if g.b.open() {
		g.a.optional = true
	}
	return g
}

// Parse sets the function that converts the argument.
func (g *Arg[T]) Parse(fn func(string) (T, error)) *Arg[T] {
	if g.b.accept(fn == nil, "argument "+g.a.name, "Parse") {
		g.parse = fn
	}
	return g
}

// Check adds a check of the value. It runs for a value from the command line
// or the default, and not when the argument is unset.
func (g *Arg[T]) Check(fn func(T) error) *Arg[T] {
	if g.b.accept(fn == nil, "argument "+g.a.name, "Check") {
		g.checks = append(g.checks, fn)
	}
	return g
}

// Complete sets the function that completes the argument.
func (g *Arg[T]) Complete(fn CompleteFunc) *Arg[T] {
	if g.b.accept(fn == nil, "argument "+g.a.name, "Complete") {
		g.a.complete = fn
	}
	return g
}

// Args describes the trailing list of positional arguments.
type Args[E any] struct {
	b      *Builder
	a      *argDef
	parse  func(string) (E, error)
	checks []func([]E) error
}

// Args declares the trailing positional arguments, bound to the slice p. It
// must be the last argument and requires at least one element unless Optional
// is set. The element parser is chosen as for Flag.
func (b *Builder) Args[E any](p *[]E, name string) *Args[E] {
	g := &Args[E]{b: b}
	var elem *E
	g.parse, _ = builtinParser(elem)
	a := &argDef{name: name, pos: callerPos(1), ptr: p, variadic: true}
	g.a = a
	a.reset = func() {
		if p != nil {
			*p = nil
		}
		a.source = SourceUnset
	}
	a.set = func(raw string) error {
		v, err := g.parse(raw)
		if err == nil {
			*p = append(*p, v)
		}
		return err
	}
	a.applyDefault = func() {}
	a.validate = func() error {
		for _, c := range g.checks {
			if err := c(*p); err != nil {
				return err
			}
		}
		return nil
	}
	a.check = func() []string {
		switch {
		case p == nil:
			return []string{"field pointer is nil"}
		case g.parse == nil:
			return []string{noParser(elem, "Parse")}
		}
		return nil
	}
	b.addArg(a)
	return g
}

// Describe sets the text shown in help.
func (g *Args[E]) Describe(text string) *Args[E] {
	if g.b.open() {
		g.a.desc = text
	}
	return g
}

// Optional allows an empty list.
func (g *Args[E]) Optional() *Args[E] {
	if g.b.open() {
		g.a.optional = true
	}
	return g
}

// Parse sets the function that converts one element.
func (g *Args[E]) Parse(fn func(string) (E, error)) *Args[E] {
	if g.b.accept(fn == nil, "argument "+g.a.name, "Parse") {
		g.parse = fn
	}
	return g
}

// Check adds a check of the whole list. It does not run when the list is
// empty.
func (g *Args[E]) Check(fn func([]E) error) *Args[E] {
	if g.b.accept(fn == nil, "argument "+g.a.name, "Check") {
		g.checks = append(g.checks, fn)
	}
	return g
}

// Complete sets the function that completes one element.
func (g *Args[E]) Complete(fn CompleteFunc) *Args[E] {
	if g.b.accept(fn == nil, "argument "+g.a.name, "Complete") {
		g.a.complete = fn
	}
	return g
}
