package autotag

import (
	"log"
	"strings"
)

// autotagScheme implements the default autotag commit message scheme:
//   - [major] or #major -> major bump
//   - [minor] or #minor -> minor bump
//   - [patch] or #patch -> patch bump
//
// Anything else returns nil; callers fall back to a patch bump by default, or
// error when strict matching is enabled.
type autotagScheme struct{}

func (autotagScheme) Name() string { return "autotag" }

func (autotagScheme) ParseCommit(msg string) bumper {
	if majorRex.MatchString(msg) {
		log.Println("major bump")
		return majorBumper
	}
	if minorRex.MatchString(msg) {
		log.Println("minor bump")
		return minorBumper
	}
	if patchRex.MatchString(msg) {
		log.Println("patch bump")
		return patchBumper
	}
	return nil
}

// conventionalScheme implements the Conventional Commits v1.0.0 scheme.
// https://www.conventionalcommits.org/en/v1.0.0/#summary
//
// When strictMatch is true, commits whose type is not in the authorized
// list short-circuit to nil — even if they carry a breaking-change marker
// — so the caller can surface "no match found" rather than inferring a
// bump from an otherwise-unrecognised commit.
type conventionalScheme struct {
	strictMatch bool
}

func (conventionalScheme) Name() string { return "conventional" }

func (c conventionalScheme) ParseCommit(msg string) bumper {
	matches := findNamedMatches(conventionalCommitRex, msg)

	bumperType, authorized := conventionalCommitAuthorizedTypes[matches["type"]]
	if c.strictMatch && !authorized {
		return nil
	}

	if strings.Contains(msg, "\nBREAKING CHANGE:") {
		return majorBumper
	}
	if breaking, ok := matches["breaking"]; ok && breaking == "!" {
		return majorBumper
	}
	return bumperType
}
