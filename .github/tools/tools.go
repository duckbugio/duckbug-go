//go:build tools

// Package tools pins the versions of the command line tools CI runs, so that
// the version lives in a manifest Dependabot can see instead of in a workflow
// string nobody is watching. It is a separate module: consumers of
// github.com/duckbugio/duckbug-go never inherit these dependencies.
package tools

import _ "golang.org/x/vuln/cmd/govulncheck"
