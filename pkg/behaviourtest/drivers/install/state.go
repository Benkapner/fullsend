package install

// perRepoState is an unexported read-only snapshot used internally by
// the ensurer's cache. All fields are set at construction and never
// modified.
type perRepoState struct {
	org     string
	repo    string
	mintURL string
}
