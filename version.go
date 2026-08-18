package main

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

// version is the smbark release version, sourced from the VERSION file at
// build time so there is a single source of truth.
var version = strings.TrimSpace(versionFile)
