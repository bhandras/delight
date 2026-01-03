// Package version defines Delight CLI version information and build metadata.
//
// Build metadata (Commit/CommitHash/Dirty) should be set using -ldflags during
// compilation.
package version

import (
	"bytes"
	"fmt"
	"strings"
)

// CommitHash stores the current git commit hash of this build.
//
// This should be set using -ldflags during compilation.
var CommitHash string

// semanticAlphabet is the allowed characters from the semantic versioning
// guidelines for pre-release version and build metadata strings.
//
// In particular they MUST only contain characters in semanticAlphabet.
const semanticAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-"

// These constants define the application version and follow the semantic
// versioning 2.0.0 spec (https://semver.org/).
const (
	appMajor uint = 1
	appMinor uint = 0
	appPatch uint = 0

	// appPreRelease MUST only contain characters from semanticAlphabet per the
	// semantic versioning spec.
	appPreRelease = ""
)

// Version returns the application version as a properly formed string per the
// semantic versioning 2.0.0 spec (https://semver.org/).
func Version() string {
	return semanticVersion()
}

// RichVersion returns the semantic version along with best-effort git metadata.
//
// When available, this includes CommitHash.
func RichVersion() string {
	version := semanticVersion()
	parts := make([]string, 0, 3)
	if strings.TrimSpace(CommitHash) != "" {
		parts = append(parts, fmt.Sprintf("commit_hash=%s", strings.TrimSpace(CommitHash)))
	}
	if len(parts) == 0 {
		return version
	}
	return fmt.Sprintf("%s %s", version, strings.Join(parts, " "))
}

// semanticVersion returns the SemVer part of the version.
func semanticVersion() string {
	version := fmt.Sprintf("%d.%d.%d", appMajor, appMinor, appPatch)
	preRelease := normalizeVerString(appPreRelease, semanticAlphabet)
	if preRelease != "" {
		version = fmt.Sprintf("%s-%s", version, preRelease)
	}
	return version
}

// normalizeVerString strips characters not present in alphabet.
func normalizeVerString(str string, alphabet string) string {
	var result bytes.Buffer
	for _, r := range str {
		if strings.ContainsRune(alphabet, r) {
			result.WriteRune(r)
		}
	}
	return result.String()
}
