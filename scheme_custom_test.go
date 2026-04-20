package autotag

import (
	"regexp"
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
)

func TestLoadSchemeFile_valid(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		expectName   string
		expectRules  int
		expectFbType bumper // nil means "none"
	}{
		{
			name:         "minimal",
			path:         "testdata/schemes/valid_minimal.yaml",
			expectName:   "minimal",
			expectRules:  1,
			expectFbType: patchBumper,
		},
		{
			name:         "autotag clone",
			path:         "testdata/schemes/autotag_clone.yaml",
			expectName:   "autotag-clone",
			expectRules:  3,
			expectFbType: nil,
		},
		{
			name:         "conventional clone",
			path:         "testdata/schemes/conventional_clone.yaml",
			expectName:   "conventional-clone",
			expectRules:  4,
			expectFbType: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := LoadSchemeFile(tc.path)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectName, s.Name())

			cs, ok := s.(*customScheme)
			assert.True(t, ok, "expected *customScheme")
			assert.Equal(t, tc.expectRules, len(cs.rules))
			assert.Equal(t, tc.expectFbType, cs.fallback)
		})
	}
}

func TestLoadSchemeFile_invalid(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		errContains string
	}{
		{
			name:        "missing file",
			path:        "testdata/schemes/does_not_exist.yaml",
			errContains: "does_not_exist.yaml",
		},
		{
			name:        "malformed yaml",
			path:        "testdata/schemes/malformed.yaml",
			errContains: "parse scheme file",
		},
		{
			name:        "unknown top-level key",
			path:        "testdata/schemes/unknown_key.yaml",
			errContains: "mystery",
		},
		{
			name:        "invalid regex",
			path:        "testdata/schemes/bad_regex.yaml",
			errContains: `rule "broken"`,
		},
		{
			name:        "invalid bump value",
			path:        "testdata/schemes/bad_bump.yaml",
			errContains: "major|minor|patch|none",
		},
		{
			name:        "empty rules",
			path:        "testdata/schemes/empty_rules.yaml",
			errContains: "at least one rule",
		},
		{
			name:        "missing default",
			path:        "testdata/schemes/no_default.yaml",
			errContains: "default is required",
		},
		{
			name:        "missing name",
			path:        "testdata/schemes/no_name.yaml",
			errContains: "name is required",
		},
		{
			name:        "rule missing match",
			path:        "testdata/schemes/rule_missing_match.yaml",
			errContains: "match is required",
		},
		{
			name:        "rule missing bump",
			path:        "testdata/schemes/rule_missing_bump.yaml",
			errContains: "bump is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadSchemeFile(tc.path)
			assert.Error(t, err)
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("expected error to contain %q, got: %v", tc.errContains, err)
			}
		})
	}
}

func assertBumper(t *testing.T, want, got bumper) {
	t.Helper()
	if want != got {
		t.Fatalf("expected %T, got %T", want, got)
	}
}

func TestCustomSchemeParseCommit(t *testing.T) {
	s := &customScheme{
		name: "test",
		rules: []compiledRule{
			{name: "breaking", re: regexp.MustCompile(`^BREAKING:`), bump: majorBumper},
			{name: "feat", re: regexp.MustCompile(`^feat:`), bump: minorBumper},
			{name: "fix", re: regexp.MustCompile(`^fix:`), bump: patchBumper},
			{name: "skip", re: regexp.MustCompile(`^skip:`), bump: nil},
		},
		fallback: patchBumper,
	}

	tests := []struct {
		name   string
		msg    string
		expect bumper
	}{
		{"first match wins — breaking over feat", "BREAKING: feat: a change", majorBumper},
		{"feat matches", "feat: add thing", minorBumper},
		{"fix matches", "fix: patch thing", patchBumper},
		{"explicit skip returns nil rule bump", "skip: docs", nil},
		{"no match falls back", "chore: upgrade deps", patchBumper},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertBumper(t, tc.expect, s.ParseCommit(tc.msg))
		})
	}
}

func TestCustomSchemeParseCommit_nilFallback(t *testing.T) {
	s := &customScheme{
		name:     "no-fallback",
		rules:    []compiledRule{{name: "feat", re: regexp.MustCompile(`^feat:`), bump: minorBumper}},
		fallback: nil,
	}

	assertBumper(t, minorBumper, s.ParseCommit("feat: thing"))
	assertBumper(t, nil, s.ParseCommit("random message"))
}

// Dogfood: load autotag_clone.yaml and verify it reproduces the built-in
// autotag scheme's decisions on representative commit messages.
func TestCustomSchemeDogfood_autotag(t *testing.T) {
	s, err := LoadSchemeFile("testdata/schemes/autotag_clone.yaml")
	assert.NoError(t, err)

	assertBumper(t, majorBumper, s.ParseCommit("[major] drop thing"))
	assertBumper(t, minorBumper, s.ParseCommit("[minor] add thing"))
	assertBumper(t, patchBumper, s.ParseCommit("[patch] fix thing"))
	assertBumper(t, majorBumper, s.ParseCommit("#major drop thing"))
	assertBumper(t, nil, s.ParseCommit("no marker here"))
}

// Dogfood: load conventional_clone.yaml and verify it reproduces the built-in
// conventional scheme's decisions (in non-strict mode).
func TestCustomSchemeDogfood_conventional(t *testing.T) {
	s, err := LoadSchemeFile("testdata/schemes/conventional_clone.yaml")
	assert.NoError(t, err)

	assertBumper(t, minorBumper, s.ParseCommit("feat: allow config to extend"))
	assertBumper(t, minorBumper, s.ParseCommit("feat(lang): add polish"))
	assertBumper(t, majorBumper, s.ParseCommit("refactor!: drop Node 6"))
	assertBumper(t, majorBumper, s.ParseCommit("refactor(runtime)!: drop Node 6"))
	assertBumper(t, majorBumper, s.ParseCommit("feat: thing\n\nBREAKING CHANGE: break"))
	assertBumper(t, patchBumper, s.ParseCommit("fix: typo"))
	assertBumper(t, nil, s.ParseCommit("not a conventional commit"))
}
