package autotag

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// customSchemeFile is the on-disk YAML shape for a user-defined scheme.
type customSchemeFile struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Rules       []customRule `yaml:"rules"`
	Default     string       `yaml:"default"`
}

// customRule is a single match-and-bump entry inside a scheme file.
type customRule struct {
	Name  string `yaml:"name"`
	Match string `yaml:"match"`
	Bump  string `yaml:"bump"`
}

// customScheme is a loaded, validated scheme backed by regex rules.
// Rules are evaluated in declaration order; the first match wins. When no
// rule matches, the scheme returns its fallback bump (nil if "none").
type customScheme struct {
	name         string
	rules        []compiledRule
	fallback     bumper
	fallbackName string
}

type compiledRule struct {
	name     string
	re       *regexp.Regexp
	bump     bumper
	bumpName string
}

func (c *customScheme) Name() string { return c.name }

func (c *customScheme) ParseCommit(msg string) bumper {
	for i, r := range c.rules {
		if r.re.MatchString(msg) {
			log.Printf("matched %s: %s bump", labelFor(r.name, i), r.bumpName)
			return r.bump
		}
	}
	if c.fallback != nil {
		log.Printf("no rule matched, applying default: %s bump", c.fallbackName)
	}
	return c.fallback
}

// LoadSchemeFile reads, decodes, and validates a YAML scheme definition at
// path. All validation happens here so that per-commit parsing at runtime is
// side-effect free.
func LoadSchemeFile(path string) (Scheme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scheme file %q: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var f customSchemeFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse scheme file %q: %w", path, err)
	}

	if f.Name == "" {
		return nil, fmt.Errorf("scheme file %q: name is required", path)
	}
	if len(f.Rules) == 0 {
		return nil, fmt.Errorf("scheme file %q: at least one rule is required", path)
	}
	if f.Default == "" {
		return nil, fmt.Errorf("scheme file %q: default is required (one of major|minor|patch|none)", path)
	}

	fallback, err := resolveBump(f.Default)
	if err != nil {
		return nil, fmt.Errorf("scheme file %q: default: %w", path, err)
	}

	compiled := make([]compiledRule, 0, len(f.Rules))
	for i, r := range f.Rules {
		label := labelFor(r.Name, i)

		if r.Match == "" {
			return nil, fmt.Errorf("scheme file %q: %s: match is required", path, label)
		}
		re, err := regexp.Compile(r.Match)
		if err != nil {
			return nil, fmt.Errorf("scheme file %q: %s: invalid regex: %w", path, label, err)
		}
		if r.Bump == "" {
			return nil, fmt.Errorf("scheme file %q: %s: bump is required", path, label)
		}
		b, err := resolveBump(r.Bump)
		if err != nil {
			return nil, fmt.Errorf("scheme file %q: %s: %w", path, label, err)
		}

		compiled = append(compiled, compiledRule{name: r.Name, re: re, bump: b, bumpName: r.Bump})
	}

	return &customScheme{name: f.Name, rules: compiled, fallback: fallback, fallbackName: f.Default}, nil
}

// resolveBump maps a YAML bump string to a bumper. "none" returns a nil
// bumper, meaning the matched commit contributes no version change.
func resolveBump(s string) (bumper, error) {
	switch s {
	case "major":
		return majorBumper, nil
	case "minor":
		return minorBumper, nil
	case "patch":
		return patchBumper, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("invalid bump %q (want major|minor|patch|none)", s)
	}
}

// labelFor returns a human-readable identifier for a rule, used in both
// validation errors at load time and match logs at parse time.
func labelFor(name string, i int) string {
	if name != "" {
		return fmt.Sprintf("rule %q", name)
	}
	return fmt.Sprintf("rule[%d]", i)
}
