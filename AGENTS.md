# Agent Instructions

This file contains project-specific instructions for the coding agent.

## Git Commit Guidelines

### Commit Message Format

```
pkg: Short summary in present tense (≤50 chars)

Longer explanation if needed, wrapped at 72 characters. Explain WHY
this change is being made and any relevant context, not just WHAT
changed.
```

**Commit message rules**:
- First line: present tense ("Fix bug" not "Fixed bug")
- Prefix with package name: `db:`, `rpc:`, `multi:` (for multiple packages)
- Subject ≤50 characters
- Body wrapped at 72 characters
- Blank line between subject and body

### Commit Granularity

Prefer small, atomic commits that build independently.

Separate commits for:
- Bug fixes (one fix per commit)
- Code restructuring/refactoring
- File moves or renames
- New subsystems or features
- Integration of new functionality

### Commit Signing

Sign commits with GPG when possible:

`git commit -S -m "message"`

### Commit Message Newlines (Important)

When creating multi-line commit messages, do **not** include literal `\n`
sequences inside a `-m "..."` string. Git does not interpret escape sequences
in `-m` arguments; it will store the backslash and `n` characters literally.

Use one of these instead:

- Multiple `-m` flags (preferred): `git commit -S -m "subject" -m "body paragraph 1" -m "body paragraph 2"`
  - Note: each `-m` adds a real newline between paragraphs.
- Shell $-quoting (zsh/bash): `git commit -S -m $'subject\n\nbody line 1\nbody line 2'`
- Commit message file: `git commit -S -F /path/to/message.txt`

### Commit Message Line Length (Important)

When the agent constructs commit messages (especially via `git commit -m ...`),
it must ensure every non-empty body line is wrapped to ≤72 characters.

Preferred approach:

- Use multiple `-m` flags and keep each `-m "..."` body paragraph line ≤72
  characters.

Before finishing a commit, validate the final message formatting:

`git show -s --format='%B' HEAD | python3 -c 'import sys; bad=[(i+1,len(l),l) for i,l in enumerate(sys.stdin.read().splitlines()) if len(l)>72 and l.strip()]; print(\"OK\" if not bad else \"\\n\".join([f\"Line {i} len {n}: {l}\" for i,n,l in bad]))'`

## Go Documentation Requirements

For Go code:
- All exported functions, variables, constants, types, structs,
  interfaces, and fields must have GoDoc comments.
- GoDoc comments should be formatted to not overflow 80 columns.

## Documentation And Readability Guidelines

These guidelines exist to keep the CLI/SDK architecture maintainable as the
project grows, and to ensure behavior is understandable without running the UI
or server.

### Go Documentation

- All functions must have GoDoc comments, including unexported helpers.
  - Keep comments concise; expand only when it materially improves readability.
- All exported variables, constants, types, structs, interfaces, and methods
  must have GoDoc comments.
- Non-trivial blocks of logic (e.g. multi-branch reducers, protocol shims,
  caching/invalidation, concurrency) must include brief inline documentation
  explaining intent and invariants.

### Swift Documentation (Harness/App)

- All functions should have doc comments, including private helpers, when the
  function embodies non-obvious logic (parsing, dedupe, state application,
  async/dispatch constraints).
- Larger blocks of logic must include inline documentation explaining why the
  logic exists (especially around Go↔Swift callback constraints).

### No Magic Numbers

- Avoid magic numbers and stringly-typed constants in logic.
- Introduce named constants for:
  - timeouts/TTLs/debounce windows
  - state string discriminants (where practical)
  - queue sizes / buffer lengths
- When a numeric value is protocol-defined, reference the protocol source or
  explain it in a short comment near the constant.
