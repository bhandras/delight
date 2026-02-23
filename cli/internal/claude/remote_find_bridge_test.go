package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBridgeScriptCandidatesIncludesRepoRelativePaths ensures candidate
// generation includes repo-style `cli/scripts` paths used by local checkouts.
func TestBridgeScriptCandidatesIncludesRepoRelativePaths(t *testing.T) {
	t.Parallel()

	execDir := filepath.Join(string(filepath.Separator), "tmp", "go", "bin")
	cwd := filepath.Join(string(filepath.Separator), "work", "delight")
	sourceDir := filepath.Join(cwd, "cli", "internal", "claude")

	candidates := bridgeScriptCandidates(execDir, cwd, sourceDir)

	required := []string{
		filepath.Join(execDir, "scripts", bridgeScriptFileName),
		filepath.Join(execDir, "..", "scripts", bridgeScriptFileName),
		filepath.Join(execDir, "..", "cli", "scripts", bridgeScriptFileName),
		filepath.Join(cwd, "scripts", bridgeScriptFileName),
		filepath.Join(cwd, "cli", "scripts", bridgeScriptFileName),
		filepath.Join(sourceDir, "..", "..", "scripts", bridgeScriptFileName),
	}
	for _, requiredPath := range required {
		if !containsString(candidates, requiredPath) {
			t.Fatalf("missing bridge candidate %q in %v", requiredPath, candidates)
		}
	}
}

// TestFindFirstExistingBridgeReturnsFirstMatch validates ordered resolution.
func TestFindFirstExistingBridgeReturnsFirstMatch(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	second := filepath.Join(tempDir, "second.cjs")
	first := filepath.Join(tempDir, "first.cjs")
	if err := os.WriteFile(second, []byte("// second"), 0o644); err != nil {
		t.Fatalf("write second bridge script: %v", err)
	}
	if err := os.WriteFile(first, []byte("// first"), 0o644); err != nil {
		t.Fatalf("write first bridge script: %v", err)
	}

	candidates := []string{
		filepath.Join(tempDir, "missing.cjs"),
		second,
		first,
	}
	got, err := findFirstExistingBridge(candidates)
	if err != nil {
		t.Fatalf("find first existing bridge: %v", err)
	}
	want, err := filepath.Abs(second)
	if err != nil {
		t.Fatalf("abs second bridge script: %v", err)
	}
	if got != want {
		t.Fatalf("resolved bridge path = %q, want %q", got, want)
	}
}

// containsString reports whether target exists in values.
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

