package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/autotag-dev/autotag"
	"github.com/jessevdk/go-flags"
)

// Options holds the CLI args
type Options struct {
	JustVersion         bool   `short:"n" description:"Just output the next version, don't autotag"`
	Verbose             bool   `short:"v" description:"Enable verbose logging"`
	Branch              string `short:"b" long:"branch" description:"Git branch to scan (defaults to main, then master)" default:""`
	RepoPath            string `short:"r" long:"repo" description:"Path to the repo" default:"./" `
	PreReleaseName      string `short:"p" long:"pre-release-name" description:"create a pre-release tag"`
	PreReleaseTimestamp string `short:"T" long:"pre-release-timestamp" description:"create a pre-release tag and append a timestamp (can be: datetime|epoch)"`
	BuildMetadata       string `short:"m" long:"build-metadata" description:"optional SemVer build metadata to append to the version with '+' character"`
	Scheme              string `short:"s" long:"scheme" description:"The commit message scheme to use (can be: autotag|conventional)" default:"autotag"`
	NoVersionPrefix     bool   `short:"e" long:"empty-version-prefix" description:"Do not prepend v to version tag"`
	StrictMatch         bool   `long:"strict-match" description:"Enforce strict mode on the scheme parsers, returns error if no match is found"`
}

var opts Options

func init() {
	_, err := flags.Parse(&opts)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func main() {
	log.SetOutput(io.Discard)
	if opts.Verbose {
		log.SetOutput(os.Stderr)
	}

	r, err := autotag.NewRepo(autotag.GitRepoConfig{
		RepoPath:                  opts.RepoPath,
		Branch:                    opts.Branch,
		PreReleaseName:            opts.PreReleaseName,
		PreReleaseTimestampLayout: opts.PreReleaseTimestamp,
		BuildMetadata:             opts.BuildMetadata,
		Scheme:                    opts.Scheme,
		Prefix:                    !opts.NoVersionPrefix,
		StrictMatch:               opts.StrictMatch,
	})
	if err != nil {
		log.SetOutput(os.Stderr)
		log.Println("Error initializing: " + err.Error())
		os.Exit(1)
	}

	// Tag unless asked otherwise
	if !opts.JustVersion {
		err = r.AutoTag()
		if err != nil {
			log.SetOutput(os.Stderr)
			log.Println("Error auto updating version: " + err.Error())
			os.Exit(1)
		}
	}

	fmt.Println(r.LatestVersion())

	// TODO:(jnelson) Add -major -minor -patch flags for force bumps Fri Sep 11 10:04:20 2015
	os.Exit(0)
}
