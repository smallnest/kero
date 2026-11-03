//go:build !darwin

package daemon

// withHomePaths is a PATH as it is: an account's places for programs are
// the system's elsewhere.
func withHomePaths(path string) string { return path }
