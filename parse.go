package cli

import (
	"strings"
)

// parser applies the command-line grammar to words, left to right. It works
// with names and raw strings only; values are converted later. The same
// parser serves Run and Complete, so both read a word the same way.
type parser struct {
	app      *app
	chain    []*Command // the application down to the current command
	vals     map[*flagDef][]string
	pos      []string // positional words of the current command
	dashdash bool
	help     bool
	version  bool

	pending    *flagDef // flag waiting for its value in the next word
	pendingTok string
}

func newParser(root *Command) *parser {
	return &parser{app: root.app, chain: []*Command{root}, vals: map[*flagDef][]string{}}
}

func (p *parser) cur() *Command { return p.chain[len(p.chain)-1] }

// lookupLong finds a long flag name on the path, including negations and
// built-in flags. neg reports a --no-<name> form.
func (p *parser) lookupLong(name string) (d *flagDef, neg bool) {
	for i := len(p.chain) - 1; i >= 0; i-- {
		for _, f := range p.chain[i].flags {
			if f.name == name {
				return f, false
			}
			if f.negatable && "no-"+f.name == name {
				return f, true
			}
		}
	}
	switch {
	case name == "help":
		return p.app.help, false
	case name == "version" && p.app.version != "":
		return p.app.versionFlag, false
	}
	return nil, false
}

func (p *parser) lookupShort(r rune) *flagDef {
	for i := len(p.chain) - 1; i >= 0; i-- {
		for _, f := range p.chain[i].flags {
			if f.short == r {
				return f
			}
		}
	}
	if r == 'h' {
		return p.app.help
	}
	return nil
}

// longNames lists the visible long flag names on the path for suggestions.
func (p *parser) longNames() []string {
	var names []string
	for i := len(p.chain) - 1; i >= 0; i-- {
		for _, f := range p.chain[i].flags {
			if !f.visible() {
				continue
			}
			names = append(names, f.name)
			if f.negatable {
				names = append(names, "no-"+f.name)
			}
		}
	}
	names = append(names, "help")
	if p.app.version != "" {
		names = append(names, "version")
	}
	return names
}

func (p *parser) record(d *flagDef, raw string) {
	switch d.kind {
	case kindHelp:
		p.help = true
	case kindVersion:
		p.version = true
	default:
		p.vals[d] = append(p.vals[d], raw)
	}
}

// word processes one complete word.
func (p *parser) word(tok string) error {
	if p.pending != nil {
		p.record(p.pending, tok)
		p.pending = nil
		return nil
	}
	switch {
	case p.dashdash:
		return p.positional(tok)
	case tok == "--":
		p.dashdash = true
		return nil
	case strings.HasPrefix(tok, "--"):
		return p.long(tok)
	case len(tok) > 1 && tok[0] == '-':
		return p.shorts(tok)
	}
	c := p.cur()
	if len(c.children) == 0 {
		return p.positional(tok)
	}
	child := c.child(tok)
	if child == nil {
		var names []string
		for _, ch := range c.children {
			if ch.visible() {
				names = append(names, ch.name)
				names = append(names, ch.aliases...)
			}
		}
		if s := suggest(tok, names); s != "" {
			return inputErrorf(c, "unknown command %q (did you mean %q?)", tok, s)
		}
		return inputErrorf(c, "unknown command %q", tok)
	}
	p.chain = append(p.chain, child)
	return nil
}

func (p *parser) long(tok string) error {
	name, val, hasVal := strings.Cut(tok[2:], "=")
	d, neg := p.lookupLong(name)
	switch {
	case d == nil:
		if s := suggest(name, p.longNames()); s != "" {
			return inputErrorf(p.cur(), "unknown flag --%s (did you mean --%s?)", name, s)
		}
		return inputErrorf(p.cur(), "unknown flag --%s", name)
	case neg:
		if hasVal {
			return inputErrorf(p.cur(), "flag --%s does not take a value", name)
		}
		p.record(d, "false")
	case d.kind == kindBool:
		if !hasVal {
			val = "true"
		}
		p.record(d, val)
	case d.takesValue():
		if !hasVal {
			p.pending, p.pendingTok = d, "--"+name
			return nil
		}
		p.record(d, val)
	default: // counter, help, version
		if hasVal {
			return inputErrorf(p.cur(), "flag --%s does not take a value", name)
		}
		p.record(d, "")
	}
	return nil
}

// shorts processes -abc. Flags without a value may be grouped; the first flag
// that takes a value consumes the rest of the word or, if nothing is left, the
// next word.
func (p *parser) shorts(tok string) error {
	rs := []rune(tok[1:])
	for i, r := range rs {
		d := p.lookupShort(r)
		if d == nil {
			if len(rs) > 1 {
				return inputErrorf(p.cur(), "unknown flag -%c in %s", r, tok)
			}
			return inputErrorf(p.cur(), "unknown flag -%c", r)
		}
		if d.takesValue() {
			if rest := string(rs[i+1:]); rest != "" {
				p.record(d, rest)
			} else {
				p.pending, p.pendingTok = d, "-"+string(r)
			}
			return nil
		}
		if d.kind == kindBool {
			p.record(d, "true")
		} else {
			p.record(d, "")
		}
	}
	return nil
}

func (p *parser) positional(tok string) error {
	c := p.cur()
	n := len(c.args)
	if n == 0 || (len(p.pos) >= n && !c.args[n-1].variadic) {
		return inputErrorf(c, "unexpected argument %q", tok)
	}
	p.pos = append(p.pos, tok)
	return nil
}

// end finishes a complete command line.
func (p *parser) end() error {
	if p.pending != nil {
		return inputErrorf(p.cur(), "flag %s requires a value", p.pendingTok)
	}
	return nil
}
