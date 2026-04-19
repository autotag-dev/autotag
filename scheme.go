package autotag

// Scheme decides, for a given commit message, what kind of version bump the
// commit implies. A nil return value means the commit does not match any rule
// defined by the scheme; the caller decides what to do (e.g. fall back to a
// patch bump, or error when running in strict mode).
type Scheme interface {
	Name() string
	ParseCommit(msg string) bumper
}
