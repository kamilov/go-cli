package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type flagKind int

const (
	kindValue flagKind = iota
	kindBool
	kindCount
	kindList
	kindMap
	kindHelp
	kindVersion
)

// Source tells where the value of a flag or argument came from in the
// current run.
type Source int

const (
	// SourceUnset means no source provided a value; the field is zero.
	SourceUnset Source = iota
	SourceDefault
	SourceEnv
	SourceCommandLine
)

// CompleteFunc returns completion candidates for a value that starts with
// prefix. The library does not filter its result. Values already entered for
// the command are visible through the closure, values of ancestors through
// Parent.
type CompleteFunc = func(ctx context.Context, prefix string) ([]Candidate, error)

// flagDef is the type-independent part of a flag declaration. The typed
// descriptor fills in the operations.
type flagDef struct {
	name      string
	short     rune
	env       string
	desc      string
	kind      flagKind
	negatable bool
	required  bool
	hidden    bool
	deprec    string
	pos       string
	ptr       any
	valueName string
	choices   []string
	defText   string
	hasDef    bool
	complete  CompleteFunc
	source    Source

	reset        func()
	set          func(raw string) error // one command-line occurrence
	setEnv       func(raw string) error // the whole environment value
	applyDefault func()
	validate     func() error    // allowed values and checks of the current value
	check        func() []string // schema problems; fills the display fields
}

func (d *flagDef) takesValue() bool {
	return d.kind == kindValue || d.kind == kindList || d.kind == kindMap
}

func (d *flagDef) repeatable() bool {
	return d.kind == kindList || d.kind == kindMap || d.kind == kindCount
}

func (d *flagDef) visible() bool { return !d.hidden && d.deprec == "" }

func (b *Builder) addFlag(d *flagDef) {
	if b.sealed {
		b.cmd.app.fail(d.pos, "command %q: flag --%s declared after bind returned", b.cmd.name, d.name)
		return
	}
	b.flags = append(b.flags, d)
}

func (b *Builder) deprecate(d *flagDef, msg string) {
	if msg == "" {
		b.cmd.app.fail(callerPos(2), "flag --%s: deprecation message is empty", d.name)
		return
	}
	d.deprec = msg
}

// FlagOption is an option of Flag that restricts the type of the field:
// OneOf, Count or Negatable.
type FlagOption[T any] interface{ applyFlag(*Flag[T]) }

// ListOption is an option of List that restricts the element type: OneOf.
type ListOption[E any] interface{ applyList(*List[E]) }

// ValueOption is an option that applies both to a single-value flag and to
// the elements of a list.
type ValueOption[T any] interface {
	FlagOption[T]
	ListOption[T]
}

type oneOf[T comparable] struct{ values []T }

// OneOf restricts the value, or each element of a list, to the given values.
// For a named type the type must be given explicitly:
// cli.OneOf[Format]("text", "json").
func OneOf[T comparable](values ...T) ValueOption[T] { return oneOf[T]{slices.Clone(values)} }

func (o oneOf[T]) allows(v T) bool { return slices.Contains(o.values, v) }

func (o oneOf[T]) texts() []string {
	s := make([]string, len(o.values))
	for i, v := range o.values {
		s[i] = fmt.Sprint(v)
	}
	return s
}

func (o oneOf[T]) applyFlag(f *Flag[T]) { f.allowed, f.d.choices = o.allows, o.texts() }

func (o oneOf[T]) applyList(l *List[T]) { l.allowed, l.d.choices = o.allows, o.texts() }

type countOption struct{}

func (countOption) applyFlag(f *Flag[int]) { f.d.kind, f.d.valueName = kindCount, "" }

// Count makes an int flag a counter: each occurrence adds one, so -vvv sets 3.
// A counter takes no value on the command line.
func Count() FlagOption[int] { return countOption{} }

type negatableOption struct{}

func (negatableOption) applyFlag(f *Flag[bool]) { f.d.negatable = true }

// Negatable adds the --no-<name> form to a bool flag, which sets it to false.
func Negatable() FlagOption[bool] { return negatableOption{} }

func notOneOf(v any, choices []string) error {
	return fmt.Errorf("value %q is not one of: %s", fmt.Sprint(v), strings.Join(choices, ", "))
}

// Flag describes a single-value flag. Its methods set properties that apply
// to a field of any type and return the same descriptor.
type Flag[T any] struct {
	b       *Builder
	d       *flagDef
	parse   func(string) (T, error)
	def     T
	hasDef  bool
	allowed func(T) bool
	checks  []func(T) error
}

// Flag declares a flag bound to the field p. The parser is chosen by the field
// type: string, bool, integers, floats and time.Duration are built in, then
// encoding.TextUnmarshaler; any other type needs Parse. A bool flag and a
// counter take no value from the next word; a bool flag is set to false only
// with --name=false or, if negatable, --no-name.
func (b *Builder) Flag[T any](p *T, name string, opts ...FlagOption[T]) *Flag[T] {
	f := &Flag[T]{b: b}
	f.d = &flagDef{name: name, pos: callerPos(1), ptr: p}
	f.parse, f.d.valueName = builtinParser(p)
	if _, ok := any(p).(*bool); ok {
		f.d.kind, f.d.valueName = kindBool, ""
	}
	for _, o := range opts {
		if o != nil {
			o.applyFlag(f)
		}
	}
	d := f.d
	d.reset = func() {
		if p != nil {
			var zero T
			*p = zero
		}
		d.source = SourceUnset
	}
	d.set = func(raw string) error {
		if d.kind == kindCount {
			*any(p).(*int)++
			return nil
		}
		v, err := f.parse(raw)
		if err == nil {
			*p = v
		}
		return err
	}
	d.setEnv = func(raw string) error {
		v, err := f.parse(raw)
		if err == nil {
			*p = v
		}
		return err
	}
	d.applyDefault = func() { *p = f.def }
	d.validate = func() error { return f.validate(*p) }
	d.check = func() []string {
		var problems []string
		switch {
		case p == nil:
			return []string{"field pointer is nil"}
		case f.parse == nil:
			problems = append(problems, noParser(p, "Parse", "List", "Map"))
		}
		if f.hasDef {
			if d.required {
				problems = append(problems, "required flag cannot have a default value")
			}
			if err := f.validate(f.def); err != nil {
				problems = append(problems, "invalid default value: "+err.Error())
			}
			d.hasDef, d.defText = true, fmt.Sprint(f.def)
		}
		return problems
	}
	b.addFlag(d)
	return f
}

func (f *Flag[T]) validate(v T) error {
	if f.allowed != nil && !f.allowed(v) {
		return notOneOf(v, f.d.choices)
	}
	for _, c := range f.checks {
		if err := c(v); err != nil {
			return err
		}
	}
	return nil
}

// Short sets the one-character name, used as -c.
func (f *Flag[T]) Short(c rune) *Flag[T] {
	if f.b.open() {
		f.d.short = c
	}
	return f
}

// Env sets the environment variable read when the flag is not given on the
// command line. An empty variable counts as unset.
// The value of the variable is not repeated in error messages, but the
// error of a parser may still quote it.
func (f *Flag[T]) Env(name string) *Flag[T] {
	if f.b.open() {
		f.d.env = name
	}
	return f
}

// Describe sets the text shown in help.
func (f *Flag[T]) Describe(text string) *Flag[T] {
	if f.b.open() {
		f.d.desc = text
	}
	return f
}

// Default sets the value used when neither the command line nor the
// environment provides one. It is checked when the tree is built.
func (f *Flag[T]) Default(v T) *Flag[T] {
	if f.b.open() {
		f.def, f.hasDef = v, true
	}
	return f
}

// Parse sets the function that converts a command-line or environment string.
func (f *Flag[T]) Parse(fn func(string) (T, error)) *Flag[T] {
	if f.b.accept(fn == nil, "flag --"+f.d.name, "Parse") {
		f.parse = fn
	}
	return f
}

// Required makes the flag mandatory: the command line or the environment must
// provide a value. A required flag cannot have a default value.
func (f *Flag[T]) Required() *Flag[T] {
	if f.b.open() {
		f.d.required = true
	}
	return f
}

// Hidden hides the flag from help and completion.
func (f *Flag[T]) Hidden() *Flag[T] {
	if f.b.open() {
		f.d.hidden = true
	}
	return f
}

// Deprecated marks the flag as deprecated. It keeps working; a value from the
// command line or the environment prints a warning with the message to
// stderr.
func (f *Flag[T]) Deprecated(message string) *Flag[T] {
	if f.b.open() {
		f.b.deprecate(f.d, message)
	}
	return f
}

// Check adds a check of the value. It runs for a value from any source,
// including the default, and not when the flag is unset.
func (f *Flag[T]) Check(fn func(T) error) *Flag[T] {
	if f.b.accept(fn == nil, "flag --"+f.d.name, "Check") {
		f.checks = append(f.checks, fn)
	}
	return f
}

// Complete sets the function that completes values of the flag.
func (f *Flag[T]) Complete(fn CompleteFunc) *Flag[T] {
	if f.b.accept(fn == nil, "flag --"+f.d.name, "Complete") {
		f.d.complete = fn
	}
	return f
}

// List describes a flag bound to a slice. Each occurrence on the command line
// adds an element; the environment variable holds comma-separated elements.
type List[E any] struct {
	b       *Builder
	d       *flagDef
	parse   func(string) (E, error)
	def     []E
	hasDef  bool
	allowed func(E) bool
	checks  []func([]E) error
}

// List declares a repeatable flag bound to the slice p. The element parser is
// chosen as for Flag. Values of one source replace, not extend, the values of
// another: the command line wins over the environment, which wins over the
// default.
func (b *Builder) List[E any](p *[]E, name string, opts ...ListOption[E]) *List[E] {
	l := &List[E]{b: b}
	var elem *E
	l.parse, _ = builtinParser(elem)
	l.d = &flagDef{name: name, pos: callerPos(1), ptr: p, kind: kindList, valueName: valueName(elem)}
	for _, o := range opts {
		if o != nil {
			o.applyList(l)
		}
	}
	d := l.d
	d.reset = func() {
		if p != nil {
			*p = nil
		}
		d.source = SourceUnset
	}
	d.set = func(raw string) error {
		v, err := l.parse(raw)
		if err == nil {
			*p = append(*p, v)
		}
		return err
	}
	d.setEnv = func(raw string) error {
		for _, item := range strings.Split(raw, ",") {
			if err := d.set(item); err != nil {
				return err
			}
		}
		return nil
	}
	// The default is copied on every use so that a run changing the slice
	// does not change the default of the next run.
	d.applyDefault = func() { *p = slices.Clone(l.def) }
	d.validate = func() error { return l.validate(*p) }
	d.check = func() []string {
		var problems []string
		switch {
		case p == nil:
			return []string{"field pointer is nil"}
		case l.parse == nil:
			problems = append(problems, noParser(elem, "Parse"))
		}
		if l.hasDef {
			if d.required {
				problems = append(problems, "required flag cannot have a default value")
			}
			if err := l.validate(l.def); err != nil {
				problems = append(problems, "invalid default value: "+err.Error())
			}
			d.hasDef, d.defText = true, joinValues(l.def)
		}
		return problems
	}
	b.addFlag(d)
	return l
}

func (l *List[E]) validate(v []E) error {
	if l.allowed != nil {
		for _, e := range v {
			if !l.allowed(e) {
				return notOneOf(e, l.d.choices)
			}
		}
	}
	for _, c := range l.checks {
		if err := c(v); err != nil {
			return err
		}
	}
	return nil
}

// Short sets the one-character name, used as -c.
func (l *List[E]) Short(c rune) *List[E] {
	if l.b.open() {
		l.d.short = c
	}
	return l
}

// Env sets the environment variable with comma-separated elements, read when
// the flag is not given on the command line. An empty variable counts as
// unset.
// The value of the variable is not repeated in error messages, but the
// error of a parser may still quote it.
func (l *List[E]) Env(name string) *List[E] {
	if l.b.open() {
		l.d.env = name
	}
	return l
}

// Describe sets the text shown in help.
func (l *List[E]) Describe(text string) *List[E] {
	if l.b.open() {
		l.d.desc = text
	}
	return l
}

// Default sets the elements used when neither the command line nor the
// environment provides any. They are copied on every use.
func (l *List[E]) Default(values ...E) *List[E] {
	if l.b.open() {
		l.def, l.hasDef = slices.Clone(values), true
	}
	return l
}

// Parse sets the function that converts one element.
func (l *List[E]) Parse(fn func(string) (E, error)) *List[E] {
	if l.b.accept(fn == nil, "flag --"+l.d.name, "Parse") {
		l.parse = fn
	}
	return l
}

// Required makes the flag mandatory: the command line or the environment must
// provide a value. A required flag cannot have a default value.
func (l *List[E]) Required() *List[E] {
	if l.b.open() {
		l.d.required = true
	}
	return l
}

// Hidden hides the flag from help and completion.
func (l *List[E]) Hidden() *List[E] {
	if l.b.open() {
		l.d.hidden = true
	}
	return l
}

// Deprecated marks the flag as deprecated; see Flag.Deprecated.
func (l *List[E]) Deprecated(message string) *List[E] {
	if l.b.open() {
		l.b.deprecate(l.d, message)
	}
	return l
}

// Check adds a check of the whole slice. It runs for a value from any source,
// including the default, and not when the flag is unset.
func (l *List[E]) Check(fn func([]E) error) *List[E] {
	if l.b.accept(fn == nil, "flag --"+l.d.name, "Check") {
		l.checks = append(l.checks, fn)
	}
	return l
}

// Complete sets the function that completes one element.
func (l *List[E]) Complete(fn CompleteFunc) *List[E] {
	if l.b.accept(fn == nil, "flag --"+l.d.name, "Complete") {
		l.d.complete = fn
	}
	return l
}

// Map describes a flag bound to a map with string keys. Each occurrence is
// key=value and a repeated key replaces the value; the environment variable
// holds k=v,k2=v2.
type Map[V any] struct {
	b      *Builder
	d      *flagDef
	parse  func(string) (V, error)
	def    map[string]V
	hasDef bool
	checks []func(map[string]V) error
}

// Map declares a repeatable key=value flag bound to the map p. The value
// parser is chosen as for Flag. As with List, one source replaces another.
func (b *Builder) Map[V any](p *map[string]V, name string) *Map[V] {
	m := &Map[V]{b: b}
	var elem *V
	m.parse, _ = builtinParser(elem)
	m.d = &flagDef{name: name, pos: callerPos(1), ptr: p, kind: kindMap, valueName: "key=" + valueName(elem)}
	d := m.d
	d.reset = func() {
		if p != nil {
			*p = nil
		}
		d.source = SourceUnset
	}
	d.set = func(raw string) error {
		k, s, ok := strings.Cut(raw, "=")
		if !ok {
			return fmt.Errorf("expected key=value")
		}
		v, err := m.parse(s)
		if err != nil {
			return err
		}
		if *p == nil {
			*p = map[string]V{}
		}
		(*p)[k] = v
		return nil
	}
	d.setEnv = func(raw string) error {
		for _, item := range strings.Split(raw, ",") {
			if err := d.set(item); err != nil {
				return err
			}
		}
		return nil
	}
	// Copied on every use, as for lists.
	d.applyDefault = func() { *p = maps.Clone(m.def) }
	d.validate = func() error { return m.validate(*p) }
	d.check = func() []string {
		var problems []string
		switch {
		case p == nil:
			return []string{"field pointer is nil"}
		case m.parse == nil:
			problems = append(problems, noParser(elem, "Parse"))
		}
		if m.hasDef {
			if d.required {
				problems = append(problems, "required flag cannot have a default value")
			}
			if err := m.validate(m.def); err != nil {
				problems = append(problems, "invalid default value: "+err.Error())
			}
			keys := slices.Sorted(maps.Keys(m.def))
			parts := make([]string, len(keys))
			for i, k := range keys {
				parts[i] = k + "=" + fmt.Sprint(m.def[k])
			}
			d.hasDef, d.defText = true, strings.Join(parts, ",")
		}
		return problems
	}
	b.addFlag(d)
	return m
}

func (m *Map[V]) validate(v map[string]V) error {
	for _, c := range m.checks {
		if err := c(v); err != nil {
			return err
		}
	}
	return nil
}

// Short sets the one-character name, used as -c.
func (m *Map[V]) Short(c rune) *Map[V] {
	if m.b.open() {
		m.d.short = c
	}
	return m
}

// Env sets the environment variable in the form k=v,k2=v2, read when the flag
// is not given on the command line. An empty variable counts as unset.
// The value of the variable is not repeated in error messages, but the
// error of a parser may still quote it.
func (m *Map[V]) Env(name string) *Map[V] {
	if m.b.open() {
		m.d.env = name
	}
	return m
}

// Describe sets the text shown in help.
func (m *Map[V]) Describe(text string) *Map[V] {
	if m.b.open() {
		m.d.desc = text
	}
	return m
}

// Default sets the map used when neither the command line nor the environment
// provides any entries. It is copied on every use.
func (m *Map[V]) Default(v map[string]V) *Map[V] {
	if m.b.open() {
		m.def, m.hasDef = maps.Clone(v), true
	}
	return m
}

// Parse sets the function that converts one value; keys are strings.
func (m *Map[V]) Parse(fn func(string) (V, error)) *Map[V] {
	if m.b.accept(fn == nil, "flag --"+m.d.name, "Parse") {
		m.parse = fn
	}
	return m
}

// Required makes the flag mandatory: the command line or the environment must
// provide a value. A required flag cannot have a default value.
func (m *Map[V]) Required() *Map[V] {
	if m.b.open() {
		m.d.required = true
	}
	return m
}

// Hidden hides the flag from help and completion.
func (m *Map[V]) Hidden() *Map[V] {
	if m.b.open() {
		m.d.hidden = true
	}
	return m
}

// Deprecated marks the flag as deprecated; see Flag.Deprecated.
func (m *Map[V]) Deprecated(message string) *Map[V] {
	if m.b.open() {
		m.b.deprecate(m.d, message)
	}
	return m
}

// Check adds a check of the whole map. It runs for a value from any source,
// including the default, and not when the flag is unset.
func (m *Map[V]) Check(fn func(map[string]V) error) *Map[V] {
	if m.b.accept(fn == nil, "flag --"+m.d.name, "Check") {
		m.checks = append(m.checks, fn)
	}
	return m
}

// Complete sets the function that completes one key=value entry.
func (m *Map[V]) Complete(fn CompleteFunc) *Map[V] {
	if m.b.accept(fn == nil, "flag --"+m.d.name, "Complete") {
		m.d.complete = fn
	}
	return m
}

func joinValues[E any](v []E) string {
	s := make([]string, len(v))
	for i, e := range v {
		s[i] = fmt.Sprint(e)
	}
	return strings.Join(s, ",")
}

// noParser explains a missing parser. The type name comes from fmt, which is
// enough to recognise a slice or a map and point to the right declaration.
func noParser(p any, alternatives ...string) string {
	t := strings.TrimPrefix(fmt.Sprintf("%T", p), "*")
	switch {
	case strings.HasPrefix(t, "[]") && len(alternatives) > 1:
		return fmt.Sprintf("no parser for slice type %s; declare it with %s", t, alternatives[1])
	case strings.HasPrefix(t, "map[") && len(alternatives) > 2:
		return fmt.Sprintf("no parser for map type %s; declare it with %s", t, alternatives[2])
	}
	return fmt.Sprintf("no parser for type %s; set one with %s", t, alternatives[0])
}
