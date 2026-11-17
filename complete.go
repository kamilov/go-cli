package cli

import (
	"context"
	"strings"
)

// Candidate is one completion candidate. For a flag value it is the value
// alone, without the flag name; joining it with the word is up to the shell
// layer.
type Candidate struct {
	Value       string
	Description string
}

// CompletionKind tells what the last word completes.
type CompletionKind int

const (
	// CompleteNone means there is nothing to complete.
	CompleteNone CompletionKind = iota
	CompleteCommand
	CompleteFlagName
	CompleteFlagValue
	CompleteArg
)

// Completion is the result of Complete.
type Completion struct {
	Kind CompletionKind
	// Name is the flag name without dashes for CompleteFlagValue and the
	// argument name for CompleteArg.
	Name string
	// Prefix is the part of the last word being completed: the start of the
	// value for CompleteFlagValue ("j" in "-oj"), the whole word otherwise.
	Prefix     string
	Candidates []Candidate
}

// Complete parses an incomplete command line and returns ways to continue
// its last word, which may be empty. args are the words after the program
// name. All words but the last follow the grammar of Run; an unknown command
// or flag among them gives an empty result without error. Values of the
// selected command and its ancestors are resolved as in Run, skipping values
// that fail to convert; required marks and checks are not verified, and no
// wrapper or run function is called. Nothing is printed.
//
// Value candidates come from the completion function of the flag or argument,
// unfiltered, otherwise from its allowed values that start with the prefix.
func (c *Command) Complete(ctx context.Context, args []string, opts ...RunOption) (Completion, error) {
	if err := c.begin(); err != nil {
		return Completion{}, err
	}
	defer c.end()
	st := newState(opts)

	if len(args) == 0 {
		args = []string{""}
	}
	p := newParser(c)
	for _, w := range args[:len(args)-1] {
		if p.word(w) != nil {
			return Completion{}, nil
		}
	}
	res, flag, arg := p.target(args[len(args)-1])

	st.chain = p.chain
	ctx = context.WithValue(ctx, stateKey{}, st)
	_ = resolve(ctx, st, p, true)

	var fn CompleteFunc
	switch {
	case flag != nil:
		fn = flag.complete
		if fn == nil {
			for _, v := range flag.choices {
				if strings.HasPrefix(v, res.Prefix) {
					res.Candidates = append(res.Candidates, Candidate{Value: v})
				}
			}
		}
	case arg != nil:
		fn = arg.complete
	}
	if fn != nil {
		cands, err := fn(ctx, res.Prefix)
		if err != nil {
			return Completion{}, err
		}
		res.Candidates = cands
	}
	return res, nil
}

// target reads the last word with the grammar of Run and tells what it
// completes. For a value it returns the flag or argument whose candidates
// are needed.
func (p *parser) target(w string) (Completion, *flagDef, *argDef) {
	if p.pending != nil {
		return p.valueOf(p.pending, w)
	}
	if p.dashdash {
		return p.argTarget(w)
	}
	if strings.HasPrefix(w, "--") {
		if name, val, ok := strings.Cut(w[2:], "="); ok {
			if d, neg := p.lookupLong(name); d != nil && !neg && d.takesValue() {
				return p.valueOf(d, val)
			}
			return Completion{}, nil, nil
		}
		return p.flagNames(w), nil, nil
	}
	if len(w) > 1 && w[0] == '-' {
		rs := []rune(w[1:])
		var last *flagDef
		for i, r := range rs {
			d := p.lookupShort(r)
			if d == nil {
				return p.flagNames(w), nil, nil
			}
			if d.takesValue() {
				if rest := string(rs[i+1:]); rest != "" {
					return p.valueOf(d, rest)
				}
				// The word ends with a flag that waits for a value: it is a
				// complete name, and the value is completed in the next word.
				return Completion{Kind: CompleteFlagName, Prefix: w, Candidates: []Candidate{{w, d.desc}}}, nil, nil
			}
			last = d
		}
		return Completion{Kind: CompleteFlagName, Prefix: w, Candidates: []Candidate{{w, last.desc}}}, nil, nil
	}
	if w == "-" {
		return p.flagNames(w), nil, nil
	}
	if c := p.cur(); len(c.children) > 0 {
		res := Completion{Kind: CompleteCommand, Prefix: w}
		for _, ch := range c.children {
			if ch.visible() && strings.HasPrefix(ch.name, w) {
				res.Candidates = append(res.Candidates, Candidate{ch.name, firstLine(ch.desc)})
			}
		}
		return res, nil, nil
	}
	return p.argTarget(w)
}

func (p *parser) valueOf(d *flagDef, prefix string) (Completion, *flagDef, *argDef) {
	return Completion{Kind: CompleteFlagValue, Name: d.name, Prefix: prefix}, d, nil
}

func (p *parser) argTarget(w string) (Completion, *flagDef, *argDef) {
	args := p.cur().args
	i := len(p.pos)
	switch {
	case i < len(args):
	case len(args) > 0 && args[len(args)-1].variadic:
		i = len(args) - 1
	default:
		return Completion{}, nil, nil
	}
	a := args[i]
	return Completion{Kind: CompleteArg, Name: a.name, Prefix: w}, nil, a
}

// flagNames offers the visible flags of the current command and its
// ancestors. A single-value flag already given is not offered again.
func (p *parser) flagNames(w string) Completion {
	res := Completion{Kind: CompleteFlagName, Prefix: w}
	add := func(name, desc string) {
		if strings.HasPrefix(name, w) {
			res.Candidates = append(res.Candidates, Candidate{name, desc})
		}
	}
	offer := func(d *flagDef) {
		add("--"+d.name, d.desc)
		if d.negatable {
			add("--no-"+d.name, d.desc)
		}
		if d.short != 0 {
			add("-"+string(d.short), d.desc)
		}
	}
	for i := len(p.chain) - 1; i >= 0; i-- {
		for _, d := range p.chain[i].flags {
			if d.visible() && (d.repeatable() || len(p.vals[d]) == 0) {
				offer(d)
			}
		}
	}
	if !p.help {
		offer(p.app.help)
	}
	if p.app.version != "" && !p.version {
		offer(p.app.versionFlag)
	}
	return res
}
