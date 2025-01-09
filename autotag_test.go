package autotag

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/alecthomas/assert"
	"github.com/gogs/git-module"
)

func init() {
	// fixed point-in-time time.Now() for testing
	timeNow = func() time.Time {
		return time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	}
}

// testRepoSetup provides a method for setting up a new git repo in a temporary directory
type testRepoSetup struct {
	// (optional) versioning scheme to use, eg: "" or "autotag", "conventional". If not set, defaults to "" (autotag)
	scheme string

	// (optional) branch to create. If not set, defaults to "master"
	branch string

	// (optional) initial tag. If not set, defaults to "v0.0.1"
	initialTag string

	// (optional) extra tags to add to the repo
	extraTags []string

	// (optional) the prerelease name to use, eg "pre". If not set, no prerelease name will be used
	preReleaseName string

	// (optional) the prerelease timestamp format to use, eg: "epoch". If not set, no prerelease timestamp will be used
	preReleaseTimestampLayout string

	// (optional) build metadata to append to the version
	buildMetadata string

	// (optional) prepend literal 'v' to version tags (default: true)
	disablePrefix bool

	// (optional) commit message to use for the next, untagged commit. Settings this allows for testing the
	// commit message parsing logic. eg: "#major this is a major commit"
	nextCommit string

	// (optional) Supply a list of commits to apply so you can test the logic between to possible tags where they may be more complex multiple bumps
	commitList []string

	// (optional) will enforce conventions and return an error if parsers don't find anything (default: false)
	strictMatch bool
}

// newTestRepo creates a new git repo in a temporary directory and returns an autotag.GitRepo struct for
// testing the autotag package.
// You must call cleanupTestRepo(t, r.repo) to remove the temporary directory after running tests.
func newTestRepo(t *testing.T, setup testRepoSetup) (GitRepo, error) {
	t.Helper()

	tr := createTestRepo(t, setup.branch)

	repo, err := git.Open(tr)
	checkFatal(t, err)

	branch := setup.branch
	if branch == "" {
		branch = "main"
	}

	tag := setup.initialTag
	if setup.initialTag == "" {
		tag = "v0.0.1"
		if setup.disablePrefix {
			tag = "0.0.1"
		}
	}
	seedTestRepo(t, tag, repo)

	if len(setup.extraTags) > 0 {
		for _, t := range setup.extraTags {
			makeTag(repo, t)
		}
	}

	if setup.nextCommit != "" {
		updateReadme(t, repo, setup.nextCommit)
	}

	if len(setup.commitList) != 0 {
		for _, c := range setup.commitList {
			updateReadme(t, repo, c)
		}
	}

	r, err := NewRepo(GitRepoConfig{
		RepoPath:                  repo.Path(),
		Branch:                    branch,
		PreReleaseName:            setup.preReleaseName,
		PreReleaseTimestampLayout: setup.preReleaseTimestampLayout,
		BuildMetadata:             setup.buildMetadata,
		Scheme:                    setup.scheme,
		Prefix:                    !setup.disablePrefix,
		StrictMatch:               setup.strictMatch,
	})
	if err != nil {
		return GitRepo{}, err
	}

	return *r, nil
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       GitRepoConfig
		shouldErr bool
	}{
		{
			name: "invalid build metadata",
			cfg: GitRepoConfig{
				Branch:        "master",
				BuildMetadata: "foo..bar",
			},
			shouldErr: true,
		},
		{
			name: "invalid build metadata - purely empty identifier",
			cfg: GitRepoConfig{
				Branch:        "master",
				BuildMetadata: "...",
			},
			shouldErr: true,
		},
		{
			name: "invalid pre-release-name - leading zero",
			cfg: GitRepoConfig{
				Branch:         "master",
				PreReleaseName: "024",
			},
			shouldErr: true,
		},
		{
			name: "invalid pre-release-name - empty identifier",
			cfg: GitRepoConfig{
				Branch:         "master",
				PreReleaseName: "...",
			},
			shouldErr: true,
		},
		{
			name: "invalid pre-release-timestamp",
			cfg: GitRepoConfig{
				Branch:                    "master",
				PreReleaseTimestampLayout: "foo",
			},
			shouldErr: true,
		},
		{
			name: "valid config with all options used",
			cfg: GitRepoConfig{
				Branch:                    "master",
				PreReleaseName:            "foo",
				PreReleaseTimestampLayout: "epoch",
				BuildMetadata:             "g12345678",
				Prefix:                    true,
				StrictMatch:               true,
			},
			shouldErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConfig(tc.cfg)
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewRepo(t *testing.T) {
	newRepoTests := []struct {
		createBranch  string
		requestBranch string
		expectBranch  string
	}{
		{"main", "main", "main"},
		{"main", "", "main"},
		{"master", "master", "master"},
		{"master", "", "master"},
	}

	for _, tt := range newRepoTests {
		tr := createTestRepo(t, tt.createBranch)

		repo, err := git.Open(tr)
		checkFatal(t, err)

		tag := "v0.0.1"
		seedTestRepo(t, tag, repo)

		r, err := NewRepo(GitRepoConfig{
			Branch:   tt.requestBranch,
			RepoPath: repo.Path(),
		})
		if err != nil {
			t.Fatal("Error creating repo: ", err)
		}

		if r.branch != tt.expectBranch {
			t.Fatalf("Expected branch %s, got [%s]", tt.expectBranch, r.branch)
		}
	}
}

func TestNewRepoMainAndMaster(t *testing.T) {
	// create repo w/"master" branch
	tr := createTestRepo(t, "master")

	repo, err := git.Open(tr)
	checkFatal(t, err)

	seedTestRepo(t, "v0.0.1", repo)

	// also create "main" branch
	f := repoRoot(repo) + "/main"
	err = exec.Command("touch", f).Run()
	if err != nil {
		fmt.Println("FAILED to touch the file ", f, err)
		checkFatal(t, err)
	}

	cmd := exec.Command("git", "checkout", "-b", "main")
	cmd.Dir = repoRoot(repo)
	err = cmd.Run()
	if err != nil {
		fmt.Println("FAILED to create/checkout main branch", err)
		checkFatal(t, err)
	}

	makeCommit(repo, "this is a commit on main")
	makeTag(repo, "v0.2.1")

	// check results
	newRepoTests := []struct {
		requestBranch string
		expectBranch  string
	}{
		{"main", "main"},
		{"master", "master"},
		{"", "main"},
	}

	for _, tt := range newRepoTests {
		r, err := NewRepo(GitRepoConfig{
			Branch:   tt.requestBranch,
			RepoPath: repo.Path(),
		})
		if err != nil {
			t.Fatal("Error creating repo: ", err)
		}

		if r.branch != tt.expectBranch {
			t.Fatalf("Expected branch %s, got [%s]", tt.expectBranch, r.branch)
		}
	}
}

func TestNewRepoStrictMatch(t *testing.T) {
	tests := []struct {
		name  string
		setup testRepoSetup
	}{
		// tests for autotag (default) scheme
		{
			name: "autotag scheme, bad type commit fails with strict match",
			setup: testRepoSetup{
				scheme:      "autotag",
				initialTag:  "v1.0.0",
				nextCommit:  "[foo]: thing 1",
				strictMatch: true,
			},
		},
		{
			name: "autotag scheme, fails to tag same commit twice with strict match",
			setup: testRepoSetup{
				scheme:      "autotag",
				initialTag:  "v1.0.0",
				strictMatch: true,
			},
		},

		// tests for conventional commits scheme. Based on:
		{
			name: "conventional commits, bad type commit fails with strict match",
			setup: testRepoSetup{
				scheme:      "conventional",
				initialTag:  "v1.0.0",
				nextCommit:  "foo: thing 1",
				strictMatch: true,
			},
		},
		{
			name: "conventional commits, bad type with breaking change fails with strict match",
			setup: testRepoSetup{
				scheme:      "conventional",
				initialTag:  "v1.0.0",
				nextCommit:  "foo: allow provided config object to extend other configs\n\nbody before footer\n\nBREAKING CHANGE: non-backwards compatible",
				strictMatch: true,
			},
		},
		{
			name: "conventional commits, bad type with ! fails with strict match",
			setup: testRepoSetup{
				scheme:      "conventional",
				initialTag:  "v1.0.0",
				nextCommit:  "foo!: thing 1",
				strictMatch: true,
			},
		},
		{
			name: "conventional commits, fails to tag same commit twice with strict match",
			setup: testRepoSetup{
				scheme:      "conventional",
				initialTag:  "v1.0.0",
				strictMatch: true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newTestRepo(t, tc.setup)
			assert.Error(t, err)
		})
	}
}

func TestMajor(t *testing.T) {
	r, err := newTestRepo(t, testRepoSetup{
		branch:     "master",
		initialTag: "v1.0.1",
	})
	if err != nil {
		t.Fatal("Error creating repo: ", err)
	}
	defer cleanupTestRepo(t, r.repo)

	v, err := r.MajorBump()
	if err != nil {
		t.Fatal("MajorBump failed: ", err)
	}

	if v.String() != "2.0.0" {
		t.Fatalf("MajorBump failed expected '2.0.0' got '%s' ", v)
	}

	fmt.Printf("Major is now %s\n", v)
}

func TestMajorWithMain(t *testing.T) {
	r, err := newTestRepo(t, testRepoSetup{
		branch:     "main",
		initialTag: "v1.0.1",
	})
	if err != nil {
		t.Fatal("Error creating repo: ", err)
	}
	defer cleanupTestRepo(t, r.repo)

	v, err := r.MajorBump()
	if err != nil {
		t.Fatal("MajorBump failed: ", err)
	}

	if v.String() != "2.0.0" {
		t.Fatalf("MajorBump failed expected '2.0.0' got '%s' ", v)
	}

	fmt.Printf("Major is now %s\n", v)
}

func TestMinor(t *testing.T) {
	r, err := newTestRepo(t, testRepoSetup{
		initialTag: "v1.0.1",
	})
	if err != nil {
		t.Fatal("Error creating repo: ", err)
	}
	defer cleanupTestRepo(t, r.repo)

	v, err := r.MinorBump()
	if err != nil {
		t.Fatal("MinorBump failed: ", err)
	}

	if v.String() != "1.1.0" {
		t.Fatalf("MinorBump failed expected '1.1.0' got '%s' \n", v)
	}
}

func TestPatch(t *testing.T) {
	r, err := newTestRepo(t, testRepoSetup{
		initialTag: "v1.0.1",
	})
	if err != nil {
		t.Fatal("Error creating repo: ", err)
	}
	defer cleanupTestRepo(t, r.repo)

	v, err := r.PatchBump()
	if err != nil {
		t.Fatal("PatchBump failed: ", err)
	}

	if v.String() != "1.0.2" {
		t.Fatalf("PatchBump failed expected '1.0.2' got '%s' \n", v)
	}
}

func TestMissingInitialTag(t *testing.T) {
	tr := createTestRepo(t, "")
	repo, err := git.Open(tr)
	checkFatal(t, err)
	defer cleanupTestRepo(t, repo)

	updateReadme(t, repo, "a commit before any usable tag has been created")

	_, err = NewRepo(GitRepoConfig{
		RepoPath: repo.Path(),
		Branch:   "master",
	})
	assert.Error(t, err)
}

func TestAutoTag(t *testing.T) {
	tests := []struct {
		name        string
		setup       testRepoSetup
		shouldErr   bool
		expectedTag string
	}{
		// tests for autotag (default) scheme
		{
			name: "autotag scheme, [major] bump",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "[major] this is a big release\n\nfoo bar baz\n",
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "autotag scheme, [minor] bump",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "[minor] this is a smaller release\n\nfoo bar baz\n",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.1.0",
		},
		{
			name: "autotag scheme, patch bump",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "this is just a basic change\n\nfoo bar baz\n",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.0.1",
		},
		{
			name: "autotag scheme, #major bump",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "#major this is a big release\n\nfoo bar baz\n",
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "autotag scheme, #minor bump",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "#minor this is a smaller release\n\nfoo bar baz\n",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.1.0",
		},
		{
			name: "pre-release-name with patch bump",
			setup: testRepoSetup{
				scheme:         "autotag",
				nextCommit:     "#patch bump",
				initialTag:     "v1.0.0",
				preReleaseName: "dev",
			},
			expectedTag: "v1.0.1-dev",
		},
		{
			name: "epoch pre-release-timestamp",
			setup: testRepoSetup{
				scheme:                    "autotag",
				nextCommit:                "#patch bump",
				initialTag:                "v1.0.0",
				preReleaseTimestampLayout: "epoch",
			},
			expectedTag: fmt.Sprintf("v1.0.1-%d", timeNow().UTC().Unix()),
		},
		{
			name: "datetime pre-release-timestamp",
			setup: testRepoSetup{
				scheme:                    "autotag",
				nextCommit:                "#patch bump",
				initialTag:                "v1.0.0",
				preReleaseTimestampLayout: "datetime",
			},
			expectedTag: fmt.Sprintf("v1.0.1-%s", timeNow().Format(datetimeTsLayout)),
		},
		{
			name: "epoch pre-release-timestamp and pre-release-name",
			setup: testRepoSetup{
				scheme:                    "autotag",
				nextCommit:                "#patch bump",
				initialTag:                "v1.0.0",
				preReleaseName:            "dev",
				preReleaseTimestampLayout: "epoch",
			},
			expectedTag: fmt.Sprintf("v1.0.1-dev.%d", timeNow().UTC().Unix()),
		},
		{
			name: "ignore non-conforming tags",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "#patch bump",
				initialTag: "v1.0.0",
				extraTags:  []string{"foo", ""},
			},
			expectedTag: "v1.0.1",
		},
		{
			name: "test with pre-relase tag",
			setup: testRepoSetup{
				scheme:     "autotag",
				nextCommit: "#patch bump",
				initialTag: "v1.0.0",
				extraTags:  []string{"v1.0.1-pre"},
			},
			expectedTag: "v1.0.1",
		},
		{
			name: "build metadata",
			setup: testRepoSetup{
				scheme:        "autotag",
				nextCommit:    "#patch bump",
				initialTag:    "v1.0.0",
				buildMetadata: "g012345678",
			},
			expectedTag: "v1.0.1+g012345678",
		},
		{
			name: "autotag scheme, [major] bump without prefix",
			setup: testRepoSetup{
				scheme:        "autotag",
				nextCommit:    "[major] this is a big release\n\nfoo bar baz\n",
				initialTag:    "1.0.0",
				disablePrefix: true,
			},
			expectedTag: "2.0.0",
		},
		{
			name: "autotag scheme, Bump with Major with interstitial minor changes",
			setup: testRepoSetup{
				scheme:        "autotag",
				initialTag:    "1.0.0",
				disablePrefix: true,
				commitList: []string{
					"#patch: thing 1",
					"[minor]: break thing 1",
					"feat: thing 2",
					"[major]: drop support for Node 6",
				},
			},
			expectedTag: "2.0.0",
		},
		{
			name: "autotag scheme, Bump with Major between minor changes",
			setup: testRepoSetup{
				scheme:        "autotag",
				initialTag:    "1.0.0",
				disablePrefix: true,
				commitList: []string{
					"[minor]: thing 1",
					"[major]: drop support for Node 6",
					"[minor]: thing 2",
				},
			},
			expectedTag: "2.0.0",
		},
		{
			name: "autotag scheme, version comparison is not lexicographic",
			setup: testRepoSetup{
				scheme:     "autotag",
				initialTag: "v0.9.0",
				commitList: []string{
					"[minor]: thing 1",
					"[minor]: thing 2",
				},
			},
			expectedTag: "v0.10.0",
		},

		// tests for conventional commits scheme. Based on:
		// https://www.conventionalcommits.org/en/v1.0.0/#summary
		// and
		// https://www.conventionalcommits.org/en/v1.0.0/#examples
		{
			name: "conventional commits, minor bump without scope",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "feat: allow provided config object to extend other configs",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.1.0",
		},
		{
			name: "conventional commits, minor bump with scope",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "feat(lang): add polish language",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.1.0",
		},
		{
			name: "conventional commits, breaking change via ! appended to type",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "refactor!: drop support for Node 6",
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "conventional commits, breaking change via ! appended to type/scope",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "refactor(runtime)!: drop support for Node 6",
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "conventional commits, breaking change via footer",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "feat: allow provided config object to extend other configs\n\nbody before footer\n\nBREAKING CHANGE: non-backwards compatible",
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "conventional commits, patch/minor bump",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "fix: correct minor typos in code",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.0.1",
		},
		{
			name: "conventional commits, non-conforming fallback to patch bump",
			setup: testRepoSetup{
				scheme:     "conventional",
				nextCommit: "not a conventional commit message",
				initialTag: "v1.0.0",
			},
			expectedTag: "v1.0.1",
		},
		{
			name: "conventional commits, breaking change with minor interstitial commits",
			setup: testRepoSetup{
				scheme: "conventional",
				commitList: []string{
					"feat: thing 1",
					"feat!: break thing 1",
					"feat: thing 2",
					"refactor(runtime)!: drop support for Node 6",
				},
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "conventional commits, breaking change between minor commits",
			setup: testRepoSetup{
				scheme: "conventional",
				commitList: []string{
					"feat: thing 1",
					"feat!: break thing 1",
					"feat: thing 2",
				},
				initialTag: "v1.0.0",
			},
			expectedTag: "v2.0.0",
		},
		{
			name: "conventional commits, version comparison is not lexicographic",
			setup: testRepoSetup{
				scheme: "conventional",
				commitList: []string{
					"feat: thing 1",
					"feat: thing 2",
				},
				initialTag: "v0.9.0",
			},
			expectedTag: "v0.10.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newTestRepo(t, tc.setup)
			checkFatal(t, err)
			defer cleanupTestRepo(t, r.repo)

			err = r.AutoTag()
			if tc.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			tags, err := r.repo.Tags()
			checkFatal(t, err)
			assert.Contains(t, tags, tc.expectedTag)
		})
	}
}

func TestValidateSemVerBuildMetadata(t *testing.T) {
	tests := []struct {
		name  string
		meta  string
		valid bool
	}{
		{
			name:  "valid single-part metadata",
			meta:  "g123456",
			valid: true,
		},
		{
			name:  "valid multi-part metadata",
			meta:  "g123456.20200512",
			valid: true,
		},
		{
			name:  "invalid characters",
			meta:  "g123456,foo_bar",
			valid: false,
		},
		{
			name:  "empty string",
			meta:  "",
			valid: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			valid := validateSemVerBuildMetadata(tc.meta)
			assert.Equal(t, tc.valid, valid)
		})
	}
}
