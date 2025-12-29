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

