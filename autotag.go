package autotag

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gogs/git-module"
	"github.com/hashicorp/go-version"
)

const (
	// datetimeTsLayout is the YYYYMMDDHHMMSS time format
	datetimeTsLayout = "20060102150405"
)

var (
	// autotag commit message scheme:
	majorRex = regexp.MustCompile(`(?i)\[major\]|\#major`)
	minorRex = regexp.MustCompile(`(?i)\[minor\]|\#minor`)
	patchRex = regexp.MustCompile(`(?i)\[patch\]|\#patch`)

	// conventional commit message scheme:
	// https://regex101.com/r/XciTmT/2
	conventionalCommitRex = regexp.MustCompile(`^\s*(?P<type>\w+)(?P<scope>(?:\([^()\r\n]*\)|\()?(?P<breaking>!)?)(?P<subject>:.*)?`)
	// conventional commit authorized types:
	conventionalCommitAuthorizedTypes = map[string]bumper{
		"feat":     minorBumper,
		"build":    patchBumper,
		"chore":    patchBumper,
		"ci":       patchBumper,
		"docs":     patchBumper,
		"fix":      patchBumper,
		"perf":     patchBumper,
		"refactor": patchBumper,
		"revert":   patchBumper,
		"style":    patchBumper,
		"test":     patchBumper,
	}

	// versionRex matches semVer style versions, eg: `v1.0.0`
	versionRex = regexp.MustCompile(`^v?([\d]+\.?.*)`)

	// semVerPreReleaseName validates SemVer according to
	// https://semver.org/#spec-item-9
	semVerPreReleaseName = regexp.MustCompile(`^[0-9A-Za-z-]+$`)

	// semVerBuildMetaRex validates SemVer build metadata strings according to
	// https://semver.org/#spec-item-10
	semVerBuildMetaRex = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
)

var timeNow = time.Now

// GitRepoConfig is the configuration needed to create a new *GitRepo.
type GitRepoConfig struct {
	// Repo is the path to the root of the git repository.
	RepoPath string

	// Branch is the name of the git branch to be tracked for tags. This value
	// must be provided.
	Branch string

	// PreReleaseName is the optional string to be appended to a tag being
	// generated (e.g., v.1.2.3-pre) to indicate the pre-release type.
	//
	// You can provide any string you want (that is valid for a Git tag); here
	// are some recommended values:
	//
	// 		* pre(release)
	// 		* alpha
	// 		* beta
	// 		* rc
	PreReleaseName string

	// PreReleaseTimestampLayout is the optional value that's used to append a
	// timestamp to the git tag. The timezone will always be UTC. This value can
	// either be the string `epoch` to be the UNIX epoch, or a Golang time
	// layout string:
	//
	// * https://golang.org/pkg/time/#pkg-constants
	//
	// If PreReleaseName is an empty string, the timestamp will be appended
	// directly to the SemVer tag:
	//
	// 		v1.2.3-1499308568
	//
	// Assuming PreReleaseName is set to `pre`, the timestamp is appended to
	// that value separated by a period (`.`):
	//
	// 		v1.2.3-pre.1499308568
	PreReleaseTimestampLayout string

	// BuildMetadata is an optional string appended by a plus sign and a series of dot separated
	// identifiers immediately following the patch or pre-release version. Identifiers MUST comprise
	// only ASCII alphanumerics and hyphen [0-9A-Za-z-]. Identifiers MUST NOT be empty. Build metadata
	// MUST be ignored when determining version precedence. Thus two versions that differ only in the
	// build metadata, have the same precedence. Examples: 1.0.0-alpha+001, 1.0.0+20130313144700,
	// 1.0.0-beta+exp.sha.5114f85
	// https://semver.org/#spec-item-10
	BuildMetadata string

	// Scheme is the versioning scheme to use when determining the version of the next
	// tag. If not specified the default "autotag" is used.
	//
	//   * "autotag" (default if not specified):
	//
	//     A git commit message header containing one of the following:
	//      * [major] or #major: major version bump
	//      * [minor] or #minor: minor version bump
	//      * [patch] or #patch: patch version bump
	//      * none of the above: patch version bump
	//
	//   * "conventional" implements the Conventional Commits v1.0.0 scheme.
	//     * https://www.conventionalcommits.org/en/v1.0.0/#summary w
	Scheme string

	// Prefix prepends literal 'v' to the tag, eg: v1.0.0. Enabled by default
	Prefix bool

	// StrictMatch enforces strict mode on the scheme parsers, returning an error if no match is found.
	// This is useful for CI/CD pipelines where you want to ensure that the commit message adheres to the scheme.
	// Disabled by default.
	StrictMatch bool

	// BuildNumber enforces append build number in metadata (after '+' character), returns error if metadata is not a unsigned integer or empty.
	// Disabled by default.
	BuildNumber bool
}

// GitRepo represents a repository we want to run actions against
type GitRepo struct {
	repo *git.Repository

	currentVersion *version.Version
	currentTag     *git.Commit
	newVersion     *version.Version
	branch         string
	branchID       string // commit id of the branch latest commit (where we will apply the tag)

	preReleaseName            string
	preReleaseTimestampLayout string
	buildMetadata             string

	scheme      string
	strictMatch bool

	prefix bool

	buildNumber bool
}

// NewRepo is a constructor for a repo object, parsing the tags that exist
func NewRepo(cfg GitRepoConfig) (*GitRepo, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	if cfg.PreReleaseTimestampLayout == "datetime" {
		cfg.PreReleaseTimestampLayout = datetimeTsLayout
	}

	gitDirPath, err := generateGitDirPath(cfg.RepoPath)
	if err != nil {
		return nil, err
	}

	if _, err := os.Stat(gitDirPath); os.IsNotExist(err) {
		return nil, err
	}

	log.Println("Opening repo at", gitDirPath)
	repo, err := git.Open(gitDirPath)
	if err != nil {
		return nil, err
	}

	if cfg.Branch == "" {
		branches, err := repo.Branches()
		if err != nil {
			return nil, err
		}

		// Locate main or master branch.
		// If main is found, stop searching and use it.
		// If master is found first, store it, but keep searching for main.
		for _, b := range branches {
			if b == "main" {
				cfg.Branch = "main"
				break
			}
			if b == "master" {
				cfg.Branch = "master"
			}
		}
		if cfg.Branch == "" {
			return nil, fmt.Errorf("no main or master branch found")
		}
	}

	r := &GitRepo{
		repo:                      repo,
		branch:                    cfg.Branch,
		preReleaseName:            cfg.PreReleaseName,
		preReleaseTimestampLayout: cfg.PreReleaseTimestampLayout,
		buildMetadata:             cfg.BuildMetadata,
		scheme:                    cfg.Scheme,
		prefix:                    cfg.Prefix,
		strictMatch:               cfg.StrictMatch,
		buildNumber:               cfg.BuildNumber,
	}

	err = r.parseTags()
	if err != nil {
		return nil, err
	}

	if err = r.calcVersion(); err != nil {
		return nil, err
	}

	return r, nil
}

func validateConfig(cfg GitRepoConfig) error {
	if cfg.BuildMetadata != "" && !validateSemVerBuildMetadata(cfg.BuildMetadata) {
		return fmt.Errorf("'%s' is not valid SemVer build metadata", cfg.BuildMetadata)
	}

	if cfg.BuildNumber && cfg.BuildMetadata != "" {
		return fmt.Errorf("'%s' is not valid, cannot input metadata if enable build number", cfg.BuildMetadata)
	}

	if cfg.PreReleaseName != "" && !validateSemVerPreReleaseName(cfg.PreReleaseName) {
		return fmt.Errorf("'%s' is not valid SemVer pre-release name", cfg.PreReleaseName)
	}

	switch cfg.PreReleaseTimestampLayout {
	case "", "datetime", "epoch":
		// nothing -- valid values
	default:
		return fmt.Errorf("pre-release-timestamp '%s' is not valid; must be (datetime|epoch)", cfg.PreReleaseTimestampLayout)
	}

	return nil
}

func generateGitDirPath(repoPath string) (string, error) {
	absolutePath, err := filepath.Abs(repoPath)
	if err != nil {
		return "", err
	}

	return filepath.Join(absolutePath, ".git"), nil
}

// Parse tags on repo, sort them, and store the most recent revision in the repo object
func (r *GitRepo) parseTags() error {
	log.Println("Parsing repository tags")

	versions := make(map[*version.Version]*git.Commit)

	tags, err := r.repo.Tags()
	if err != nil {
		return fmt.Errorf("failed to fetch tags: %s", err.Error())
	}

	for tag, commit := range tags {
		v, err := maybeVersionFromTag(commit)
		if err != nil {
			log.Println("skipping non version tag: ", tag)
			continue
		}

		if v == nil {
			log.Println("skipping non version tag: ", tag)
			continue
		}

		c, err := r.repo.CommitByRevision(commit)
		if err != nil {
			return fmt.Errorf("error reading commit '%s':  %s", commit, err)
		}
		versions[v] = c
	}

	keys := make([]*version.Version, 0, len(versions))
	for key := range versions {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(version.Collection(keys)))

	// loop over the tags and find the last reachable non pre-release tag,
	// because we want to calculate the tag from v1.2.3 not v1.2.4-pre1.`
	for _, version := range keys {
		if len(version.Prerelease()) == 0 {
			r.currentVersion = version
			r.currentTag = versions[version]
			return nil
		}
		log.Printf("skipping pre-release tag version: %s", version.String())
	}

	return fmt.Errorf("no stable (non pre-release) version tags found")
}

func maybeVersionFromTag(tag string) (*version.Version, error) {
	if tag == "" {
		return nil, fmt.Errorf("empty tag not supported")
	}

	ver, vErr := parseVersion(tag)
	if vErr != nil {
		return nil, fmt.Errorf("couldn't parse version %s: %s", tag, vErr)
	}
	return ver, nil
}

// parseVersion returns a version object from a parsed string. This normalizes semver strings, and adds the ability to parse strings with 'v' leader. so that `v1.0.1`->     `1.0.1`  which we need for berkshelf to work
func parseVersion(v string) (*version.Version, error) {
	if versionRex.MatchString(v) {
		m := versionRex.FindStringSubmatch(v)
		if len(m) >= 2 {
			v = m[1]
		}
	}

	nVersion, err := version.NewVersion(v)
	if err != nil && nVersion != nil && len(nVersion.Segments()) >= 1 {
		return nVersion, err
	}
	return nVersion, nil
}

// LatestVersion Reports the Latest version of the given repo
// TODO:(jnelson) this could be more intelligent, looking for a nil new and reporting the latest version found if we refactor autobump at some point Mon Sep 14 13:05:49 2015
func (r *GitRepo) LatestVersion() string {
	return r.newVersion.String()
}

func (r *GitRepo) retrieveBranchInfo() error {
	id, err := r.repo.BranchCommitID(r.branch)
	if err != nil {
		return fmt.Errorf("error getting head commit: %s ", err.Error())
	}

	r.branchID = id
	return nil
}

func preReleaseVersion(v *version.Version, name, tsLayout string) (*version.Version, error) {
	if len(name) == 0 && len(tsLayout) == 0 {
		return v, nil
	}

	if len(v.Prerelease()) > 0 {
		return nil, errors.New("*version.Version already has a PreRelease value set")
	}

	buf := &bytes.Buffer{}

	if _, err := buf.WriteString(name); err != nil {
		return nil, err
	}

	if len(tsLayout) > 0 {
		// XXX(theckman): if the buffer already has content written to it, add
		// the `.` character as a delimiter. The `+` character was not used as
		// the delimiter because some systems that support version tags do not
		// allow it within the string (looking at you, Docker).
		if buf.Len() > 0 {
			if _, err := buf.WriteString("."); err != nil {
				return nil, err
			}
		}

		var (
			timestamp   string
			currentTime = timeNow().UTC()
		)

		if tsLayout == "epoch" {
			timestamp = strconv.FormatInt(currentTime.Unix(), 10)
		} else {
			timestamp = currentTime.Format(tsLayout)
		}

		if _, err := buf.WriteString(timestamp); err != nil {
			return nil, err
		}
	}

	verStr := fmt.Sprintf("%s-%s", v.String(), buf.String())
	return version.NewVersion(verStr)
}

// calcVersion looks over commits since the last tag, and will apply the version bump needed. It will patch if no other instruction is found
// it populates the repo.newVersion with the new calculated version
func (r *GitRepo) calcVersion() error {
	r.newVersion = r.currentVersion
	if err := r.retrieveBranchInfo(); err != nil {
		return err
	}

	startCommit, err := r.repo.BranchCommit(r.branch)
	if err != nil {
		return err
	}

	revList := []string{fmt.Sprintf("%s..%s", r.currentTag.ID, startCommit.ID)}

	l, err := r.repo.RevList(revList)
	if len(l) == 0 && r.strictMatch {
		return fmt.Errorf("no version to bump for the same commit")
	}
	if err != nil {
		log.Printf("Error loading history for tag '%s': %s ", r.currentVersion, err.Error())
	}

	// r.branchID is the newest commit; r.currentTag.ID is oldest
	log.Printf("Checking commits from %s to %s ", r.branchID, r.currentTag.ID)

	// Revlist returns in reverse Chronological We want chronological. Then check each commit for bump messages
	for i := len(l) - 1; i >= 0; i-- {
		commit := l[i] // getting the reverse order element
		if commit == nil {
			return fmt.Errorf("commit pointed to nil object. This should not happen")
		}

		v, nerr := r.parseCommit(commit)
		if nerr != nil {
			return nerr
		}

		if v != nil && v.GreaterThan(r.newVersion) {
			r.newVersion = v
		}
	}

	// if there is no movement on the version from commits, bump patch
	if r.newVersion.Equal(r.currentVersion) {
		if r.strictMatch {
			return fmt.Errorf("no version to bump found in commit message")
		}
		if r.newVersion, err = patchBumper.bump(r.currentVersion); err != nil {
			return err
		}
	}

	// append pre-release-name and/or pre-release-timestamp to the version
	if len(r.preReleaseName) > 0 || len(r.preReleaseTimestampLayout) > 0 {
		if r.newVersion, err = preReleaseVersion(r.newVersion, r.preReleaseName, r.preReleaseTimestampLayout); err != nil {
			return err
		}
	}

	// append optional build metadata
	if r.buildNumber {
		if r.buildMetadata != "" {
			return fmt.Errorf("cannot input custom method if enable build number")
		}

		metadata := r.currentVersion.Metadata()
		buildMetadata := ""
		if metadata == "" {
			buildMetadata = "1"
		} else {
			currentBuildNumber, err := strconv.ParseUint(metadata, 10, 64)
			if err != nil {
				return fmt.Errorf("build number must be a unsigne integer")
			}

			buildMetadata = strconv.FormatUint(currentBuildNumber+1, 10)
		}

		if r.newVersion, err = version.NewVersion(fmt.Sprintf("%s+%s", r.newVersion.String(), buildMetadata)); err != nil {
			return err
		}
	} else if r.buildMetadata != "" {
		if r.newVersion, err = version.NewVersion(fmt.Sprintf("%s+%s", r.newVersion.String(), r.buildMetadata)); err != nil {
			return err
		}
	}

	return nil
}

// AutoTag applies the new version tag thats calculated
func (r *GitRepo) AutoTag() error {
	return r.tagNewVersion()
}

func (r *GitRepo) tagNewVersion() error {
	// TODO:(jnelson) These should be configurable? Mon Sep 14 12:02:52 2015
	tagName := fmt.Sprintf("v%s", r.newVersion.String())
	if !r.prefix {
		tagName = r.newVersion.String()
	}

	log.Println("Writing Tag", tagName)
	err := r.repo.CreateTag(tagName, r.branchID)
	if err != nil {
		return fmt.Errorf("error creating tag: %s", err.Error())
	}
	return nil
}

// parseCommit looks at HEAD commit see if we want to increment major/minor/patch
func (r *GitRepo) parseCommit(commit *git.Commit) (*version.Version, error) {
	var b bumper
	msg := commit.Message
	log.Printf("Parsing %s: %s\n", commit.ID, msg)

	switch r.scheme {
	case "conventional":
		b = parseConventionalCommit(msg, r.strictMatch)
	case "", "autotag":
		b = parseAutotagCommit(msg)
	}

	if r.strictMatch && b == nil {
		return nil, fmt.Errorf("no match found for commit %s", commit.ID)
	}

	// fallback to patch bump if no matches from the scheme parsers
	if b != nil {
		return b.bump(r.currentVersion)
	}

	return nil, nil
}

// parseAutotagCommit implements the autotag (default) commit scheme.
// A git commit message header containing:
//   - [major] or #major: major version bump
//   - [minor] or #minor: minor version bump
//   - [patch] or #patch: patch version bump
//
// If no action is present nil is returned and the caller must decide what action to take.
func parseAutotagCommit(msg string) bumper {
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

// parseConventionalCommit implements the Conventional Commit scheme. Given a commit message
// A strict match option will enforce that the commit message must match the conventional commit
// it will return the correct version bumper. In the case of non-confirming conventional commit
// it will return nil and the caller will decide what action to take.
// https://www.conventionalcommits.org/en/v1.0.0/#summary
func parseConventionalCommit(msg string, strictMatch bool) bumper {
	matches := findNamedMatches(conventionalCommitRex, msg)

	// If we're in strict match and no matches are found, return nil
	bumperType, authorized := conventionalCommitAuthorizedTypes[matches["type"]]
	if strictMatch && !authorized {
		return nil
	}

	// If the commit contains a footer with 'BREAKING CHANGE:' it is always a major bump
	if strings.Contains(msg, "\nBREAKING CHANGE:") {
		return majorBumper
	}

	// If the type/scope in the header includes a trailing '!' this is a breaking change
	if breaking, ok := matches["breaking"]; ok && breaking == "!" {
		return majorBumper
	}

	// If the type in the header match a type try to find it in the authorized list
	// If it's not in the list it returns nil
	return bumperType
}

// MajorBump will bump the version one major rev 1.0.0 -> 2.0.0
func (r *GitRepo) MajorBump() (*version.Version, error) {
	return majorBumper.bump(r.currentVersion)
}

// MinorBump will bump the version one minor rev 1.1.0 -> 1.2.0
func (r *GitRepo) MinorBump() (*version.Version, error) {
	return minorBumper.bump(r.currentVersion)
}

// PatchBump will bump the version one patch rev 1.1.1 -> 1.1.2
func (r *GitRepo) PatchBump() (*version.Version, error) {
	return patchBumper.bump(r.currentVersion)
}

// findNamedMatches is a helper function for use with regexes containing named capture groups.
// It takes a regex and a string and returns a map with keys corresponding to the named captures
// in the regex. If there are no matches the map will be empty.
// https://play.golang.org/p/GR_6YHaEvef
func findNamedMatches(regex *regexp.Regexp, str string) map[string]string {
	match := regex.FindStringSubmatch(str)

	results := map[string]string{}
	for i, name := range match {
		results[regex.SubexpNames()[i]] = name
	}
	return results
}

// validateSemVerBuildMetadata validates SemVer build metadata strings according to
// https://semver.org/#spec-item-10
func validateSemVerBuildMetadata(meta string) bool {
	identifiers := strings.Split(meta, ".")

	for _, s := range identifiers {
		if s == "" || !semVerBuildMetaRex.MatchString(s) {
			return false
		}
	}
	return true
}

// validateSemVerPreReleaseName validates SemVer pre release name according to
// https://semver.org/#spec-item-9
func validateSemVerPreReleaseName(meta string) bool {
	identifiers := strings.Split(meta, ".")

	for _, s := range identifiers {
		if s == "" || strings.HasPrefix(s, "0") || !semVerPreReleaseName.MatchString(s) {
			return false
		}
	}
	return true
}
