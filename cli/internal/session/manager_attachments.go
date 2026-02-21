package session

import (
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bhandras/delight/shared/wire"
)

const (
	// attachmentUploadRootDirName is the top-level temp directory used for
	// session attachment uploads on the CLI machine.
	attachmentUploadRootDirName = "delight-attachments"

	// attachmentUploadMaxBytes bounds the maximum accepted attachment size.
	attachmentUploadMaxBytes int64 = 24 * 1024 * 1024

	// attachmentUploadChunkMaxBytes bounds each decoded chunk payload size.
	attachmentUploadChunkMaxBytes int = 256 * 1024

	// attachmentUploadPendingTTL is the retention window for unfinished uploads.
	attachmentUploadPendingTTL = 20 * time.Minute

	// attachmentUploadCommittedTTL is the retention window for committed files.
	attachmentUploadCommittedTTL = 6 * time.Hour

	// attachmentUploadPruneInterval bounds how often we walk the filesystem for
	// stale upload files.
	attachmentUploadPruneInterval = 1 * time.Minute
)

// attachmentUploadState tracks one session-scoped attachment upload.
type attachmentUploadState struct {
	uploadID       string
	sessionID      string
	fileName       string
	mimeType       string
	expectedBytes  int64
	receivedBytes  int64
	nextChunkIndex int64
	tempPath       string
	finalPath      string
	committed      bool
	updatedAt      time.Time
}

// attachmentUploadRootDir returns the machine-local root used for upload files.
func (m *Manager) attachmentUploadRootDir() string {
	return filepath.Join(os.TempDir(), attachmentUploadRootDirName)
}

// attachmentUploadBegin initializes a new attachment upload.
func (m *Manager) attachmentUploadBegin(req wire.AttachmentUploadBeginRequest) (wire.AttachmentUploadResponse, error) {
	uploadID := strings.TrimSpace(req.UploadID)
	if uploadID == "" {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("uploadId is required")
	}
	sizeBytes := req.SizeBytes
	if sizeBytes <= 0 {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("sizeBytes must be > 0")
	}
	if sizeBytes > attachmentUploadMaxBytes {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("attachment too large (max %d bytes)", attachmentUploadMaxBytes)
	}

	sessionID := strings.TrimSpace(m.sessionID)
	if sessionID == "" {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("session id unavailable")
	}

	fileName := sanitizeAttachmentFileName(req.FileName)
	if fileName == "" {
		fileName = "attachment.bin"
	}

	m.attachmentMu.Lock()
	defer m.attachmentMu.Unlock()

	now := time.Now()
	m.pruneAttachmentUploadsLocked(now)

	if _, exists := m.attachmentUploads[uploadID]; exists {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("upload already exists")
	}

	sessionDir := filepath.Join(
		m.attachmentUploadRootDir(),
		sanitizeAttachmentPathSegment(sessionID),
	)
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		return wire.AttachmentUploadResponse{}, err
	}

	tempPath := filepath.Join(sessionDir, "."+sanitizeAttachmentPathSegment(uploadID)+".part")
	if err := os.WriteFile(tempPath, nil, 0o600); err != nil {
		return wire.AttachmentUploadResponse{}, err
	}

	m.attachmentUploads[uploadID] = &attachmentUploadState{
		uploadID:      uploadID,
		sessionID:     sessionID,
		fileName:      fileName,
		mimeType:      strings.TrimSpace(req.MIMEType),
		expectedBytes: sizeBytes,
		tempPath:      tempPath,
		updatedAt:     now,
	}

	return wire.AttachmentUploadResponse{
		Success:       true,
		UploadID:      uploadID,
		BytesReceived: 0,
	}, nil
}

// attachmentUploadChunk appends one chunk to an in-flight attachment upload.
func (m *Manager) attachmentUploadChunk(req wire.AttachmentUploadChunkRequest) (wire.AttachmentUploadResponse, error) {
	uploadID := strings.TrimSpace(req.UploadID)
	if uploadID == "" {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("uploadId is required")
	}

	m.attachmentMu.Lock()
	defer m.attachmentMu.Unlock()

	now := time.Now()
	m.pruneAttachmentUploadsLocked(now)

	state, ok := m.attachmentUploads[uploadID]
	if !ok {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("upload not found")
	}
	if state.committed {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("upload already committed")
	}
	if req.ChunkIndex != state.nextChunkIndex {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("unexpected chunk index")
	}

	chunk, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("invalid chunk encoding")
	}
	if len(chunk) == 0 {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("chunk is empty")
	}
	if len(chunk) > attachmentUploadChunkMaxBytes {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("chunk too large")
	}

	nextBytes := state.receivedBytes + int64(len(chunk))
	if nextBytes > state.expectedBytes {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("upload exceeded expected size")
	}

	f, err := os.OpenFile(state.tempPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return wire.AttachmentUploadResponse{}, err
	}
	n, writeErr := f.Write(chunk)
	closeErr := f.Close()
	if writeErr != nil {
		return wire.AttachmentUploadResponse{}, writeErr
	}
	if closeErr != nil {
		return wire.AttachmentUploadResponse{}, closeErr
	}
	if n != len(chunk) {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("short chunk write")
	}

	state.receivedBytes = nextBytes
	state.nextChunkIndex++
	state.updatedAt = now

	return wire.AttachmentUploadResponse{
		Success:       true,
		UploadID:      uploadID,
		BytesReceived: state.receivedBytes,
	}, nil
}

// attachmentUploadCommit finalizes an upload and returns its local file path.
func (m *Manager) attachmentUploadCommit(req wire.AttachmentUploadCommitRequest) (wire.AttachmentUploadResponse, error) {
	uploadID := strings.TrimSpace(req.UploadID)
	if uploadID == "" {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("uploadId is required")
	}

	m.attachmentMu.Lock()
	defer m.attachmentMu.Unlock()

	now := time.Now()
	m.pruneAttachmentUploadsLocked(now)

	state, ok := m.attachmentUploads[uploadID]
	if !ok {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("upload not found")
	}
	if state.committed && strings.TrimSpace(state.finalPath) != "" {
		state.updatedAt = now
		return wire.AttachmentUploadResponse{
			Success:       true,
			UploadID:      uploadID,
			Path:          state.finalPath,
			BytesReceived: state.receivedBytes,
		}, nil
	}
	if state.receivedBytes != state.expectedBytes {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("upload incomplete")
	}

	sessionDir := filepath.Dir(state.tempPath)
	prefix := sanitizeAttachmentPathSegment(uploadID)
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	finalName := prefix + "-" + state.fileName
	finalPath := filepath.Join(sessionDir, finalName)
	finalPath = ensureUniqueAttachmentPath(finalPath)
	if err := os.Rename(state.tempPath, finalPath); err != nil {
		return wire.AttachmentUploadResponse{}, err
	}
	if err := os.Chmod(finalPath, 0o600); err != nil {
		return wire.AttachmentUploadResponse{}, err
	}

	state.finalPath = finalPath
	state.committed = true
	state.updatedAt = now

	return wire.AttachmentUploadResponse{
		Success:       true,
		UploadID:      uploadID,
		Path:          finalPath,
		BytesReceived: state.receivedBytes,
	}, nil
}

// attachmentUploadCancel cancels an upload and removes any temporary/final file.
func (m *Manager) attachmentUploadCancel(req wire.AttachmentUploadCancelRequest) (wire.AttachmentUploadResponse, error) {
	uploadID := strings.TrimSpace(req.UploadID)
	if uploadID == "" {
		return wire.AttachmentUploadResponse{}, fmt.Errorf("uploadId is required")
	}

	m.attachmentMu.Lock()
	defer m.attachmentMu.Unlock()

	now := time.Now()
	m.pruneAttachmentUploadsLocked(now)

	state, ok := m.attachmentUploads[uploadID]
	if !ok {
		return wire.AttachmentUploadResponse{
			Success:  true,
			UploadID: uploadID,
		}, nil
	}

	removeAttachmentFile(state.tempPath)
	removeAttachmentFile(state.finalPath)
	delete(m.attachmentUploads, uploadID)

	return wire.AttachmentUploadResponse{
		Success:  true,
		UploadID: uploadID,
	}, nil
}

// pruneAttachmentUploadsLocked removes stale in-memory states and stale files.
//
// Caller must hold m.attachmentMu.
func (m *Manager) pruneAttachmentUploadsLocked(now time.Time) {
	for uploadID, state := range m.attachmentUploads {
		ttl := attachmentUploadPendingTTL
		if state.committed {
			ttl = attachmentUploadCommittedTTL
		}
		if now.Sub(state.updatedAt) < ttl {
			continue
		}
		removeAttachmentFile(state.tempPath)
		removeAttachmentFile(state.finalPath)
		delete(m.attachmentUploads, uploadID)
	}

	if now.Sub(m.lastAttachmentPruneAt) < attachmentUploadPruneInterval {
		return
	}
	m.lastAttachmentPruneAt = now
	pruneAttachmentUploadRoot(m.attachmentUploadRootDir(), now)
}

// pruneAttachmentUploadRoot removes stale upload files from disk.
func pruneAttachmentUploadRoot(root string, now time.Time) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}

		ttl := attachmentUploadCommittedTTL
		if strings.HasSuffix(d.Name(), ".part") {
			ttl = attachmentUploadPendingTTL
		}
		if now.Sub(info.ModTime()) < ttl {
			return nil
		}
		_ = os.Remove(path)
		return nil
	})
}

// sanitizeAttachmentFileName normalizes an untrusted file name for local disk.
func sanitizeAttachmentFileName(name string) string {
	base := strings.TrimSpace(filepath.Base(name))
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "attachment.bin"
	}
	var b strings.Builder
	b.Grow(len(base))
	for _, r := range base {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ".")
	if out == "" {
		return "attachment.bin"
	}
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

// sanitizeAttachmentPathSegment normalizes untrusted values for directory paths.
func sanitizeAttachmentPathSegment(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "unknown"
	}
	return out
}

// ensureUniqueAttachmentPath appends a numeric suffix if path already exists.
func ensureUniqueAttachmentPath(path string) string {
	if _, err := os.Stat(path); err != nil {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
	return path
}

// removeAttachmentFile deletes a file path best-effort.
func removeAttachmentFile(path string) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return
	}
	_ = os.Remove(trimmed)
}
