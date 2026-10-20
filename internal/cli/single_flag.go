package cli

import (
	"errors"

	"github.com/spf13/pflag"
)

// repeatedFlagError is a single-value flag given twice. pflag's own string
// flag keeps the last value silently, so `--status a --status b` would filter
// by b alone and look like an AND that matched nothing; refusing it is the only
// reading that cannot be mistaken for one.
type repeatedFlagError struct {
	flag string
}

func (e *repeatedFlagError) Error() string {
	return "--" + e.flag + " given more than once"
}

// singleStringValue is a pflag.Value that accepts its flag at most once.
type singleStringValue struct {
	name string
	p    *string
	set  bool
}

func (v *singleStringValue) Set(s string) error {
	if v.set {
		return &repeatedFlagError{flag: v.name}
	}
	*v.p = s
	v.set = true
	return nil
}

func (v *singleStringValue) Type() string   { return "string" }
func (v *singleStringValue) String() string { return *v.p }

// singleStringVarP registers a string flag, defaulting to empty, that refuses
// to be repeated.
func singleStringVarP(f *pflag.FlagSet, p *string, name, shorthand, usage string) {
	*p = ""
	f.VarP(&singleStringValue{name: name, p: p}, name, shorthand, usage)
}

// repeatedFlagCause unwraps pflag's "invalid argument" wrapping so a repeated
// single-value flag is reported in its own words, under the command.
func repeatedFlagCause(err error) error {
	var repeated *repeatedFlagError
	if errors.As(err, &repeated) {
		return repeated
	}
	return err
}
