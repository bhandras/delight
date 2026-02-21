package session

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bhandras/delight/shared/logger"
)

const (
	// gitPollInterval is how often we refresh git metadata while a turn is
	// actively working.
	gitPollInterval = 60 * time.Second

	// gitStatusCommandTimeout bounds each git status command invocation.
	gitStatusCommandTimeout = 3 * time.Second
)

// gitStatusSnapshot is the normalized git state persisted into terminal
// metadata.
type gitStatusSnapshot struct {
	InRepo  bool
	Branch  string
	Added   int
	Removed int
	Dirty   bool
}

// refreshTerminalGitMetadata refreshes git metadata for the current workdir and
// optionally persists it to the server via terminal metadata update.
func (m *Manager) refreshTerminalGitMetadata(persist bool, reason string) {
	if m == nil || m.terminalMetadata == nil {
		return
	}
	workDir := strings.TrimSpace(m.workDir)
	if workDir == "" {
		return
	}

	next, err := collectGitStatusSnapshot(workDir)
	if err != nil {
		if m.debug {
			logger.Debugf("git metadata refresh (%s) failed: %v", reason, err)
		}
		return
	}

	prev := gitStatusSnapshot{
		InRepo:  m.terminalMetadata.GitInRepo,
		Branch:  strings.TrimSpace(m.terminalMetadata.GitBranch),
		Added:   m.terminalMetadata.GitAdded,
		Removed: m.terminalMetadata.GitRemoved,
		Dirty:   m.terminalMetadata.GitDirty,
	}
	if prev == next {
		return
	}

	m.terminalMetadata.GitInRepo = next.InRepo
	m.terminalMetadata.GitBranch = next.Branch
	m.terminalMetadata.GitAdded = next.Added
	m.terminalMetadata.GitRemoved = next.Removed
	m.terminalMetadata.GitDirty = next.Dirty

	if !persist {
		return
	}
	if err := m.updateTerminalMetadata(); err != nil && m.debug {
		logger.Debugf("git metadata persist (%s) failed: %v", reason, err)
	}
}

// collectGitStatusSnapshot collects git branch and delta counters for a
// workdir. Non-repos are reported as InRepo=false.
func collectGitStatusSnapshot(workDir string) (gitStatusSnapshot, error) {
	statusOut, statusErr, err := runGitCommand(
		workDir,
		gitStatusCommandTimeout,
		"status",
		"--porcelain=v2",
		"--branch",
	)
	if err != nil {
		if isNotGitRepository(statusErr) {
			return gitStatusSnapshot{InRepo: false}, nil
		}
		return gitStatusSnapshot{}, err
	}

	snapshot := parseGitStatusPorcelainV2(statusOut)
	snapshot.InRepo = true

	added, removed, err := numstatTotals(workDir, false)
	if err == nil {
		snapshot.Added += added
		snapshot.Removed += removed
	}
	addedCached, removedCached, err := numstatTotals(workDir, true)
	if err == nil {
		snapshot.Added += addedCached
		snapshot.Removed += removedCached
	}

	return snapshot, nil
}

// runGitCommand runs a git command in the target workdir and returns stdout/stderr.
func runGitCommand(workDir string, timeout time.Duration, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdArgs := append([]string{"-C", workDir}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.String(), stderr.String(), fmt.Errorf("git command timed out: %s", strings.Join(args, " "))
		}
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}

// isNotGitRepository reports whether stderr indicates a non-git directory.
func isNotGitRepository(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "not a git repository")
}

// parseGitStatusPorcelainV2 parses branch and dirtiness from porcelain v2
// output.
func parseGitStatusPorcelainV2(output string) gitStatusSnapshot {
	snapshot := gitStatusSnapshot{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "# branch.head ") {
			branch := strings.TrimSpace(strings.TrimPrefix(line, "# branch.head "))
			if branch == "(detached)" {
				branch = "detached"
			}
			snapshot.Branch = branch
			continue
		}

		// porcelain v2 record kinds:
		// 1 = ordinary changed entry
		// 2 = renamed/copied entry
		// u = unmerged entry
		// ? = untracked
		if strings.HasPrefix(line, "1 ") ||
			strings.HasPrefix(line, "2 ") ||
			strings.HasPrefix(line, "u ") ||
			strings.HasPrefix(line, "? ") {
			snapshot.Dirty = true
		}
	}
	return snapshot
}

// numstatTotals sums added/removed line counts from `git diff --numstat`.
func numstatTotals(workDir string, cached bool) (int, int, error) {
	args := []string{"diff", "--numstat"}
	if cached {
		args = append(args, "--cached")
	}

	stdout, _, err := runGitCommand(workDir, gitStatusCommandTimeout, args...)
	if err != nil {
		return 0, 0, err
	}

	addedTotal := 0
	removedTotal := 0
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		added, err := strconv.Atoi(fields[0])
		if err == nil {
			addedTotal += added
		}
		removed, err := strconv.Atoi(fields[1])
		if err == nil {
			removedTotal += removed
		}
	}
	return addedTotal, removedTotal, nil
}
