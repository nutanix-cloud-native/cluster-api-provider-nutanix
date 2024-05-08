//go:build tools
// +build tools

package tools

import (
	// Import other tools needed in the project but are not used in the code to force go mod to download them
	_ "github.com/AlekSi/gocov-xml"
	_ "github.com/jstemmer/go-junit-report"
)
