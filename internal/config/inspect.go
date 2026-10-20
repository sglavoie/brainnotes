package config

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// EffectiveValue returns a loaded, normalized field, including defaults. Only
// schema keys are inspectable; runtime metadata and arbitrary Go fields are not.
// Errors carry no command prefix: the command that asked names itself.
func (c *Config) EffectiveValue(key string) (any, error) {
	spec, ok := specFor(key)
	if !ok || spec.key != key {
		return nil, fmt.Errorf("unknown configuration key %q", key)
	}
	if c == nil {
		return nil, errors.New("no configuration loaded")
	}
	value := reflect.ValueOf(c).Elem()
	for _, part := range strings.Split(key, ".") {
		value = configField(value, part)
		if !value.IsValid() {
			return nil, fmt.Errorf("no value for %q", key)
		}
	}
	return value.Interface(), nil
}

func configField(value reflect.Value, name string) reflect.Value {
	if value.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	for i := 0; i < value.NumField(); i++ {
		if value.Type().Field(i).Tag.Get("toml") == name {
			return value.Field(i)
		}
	}
	return reflect.Value{}
}
