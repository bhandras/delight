package cli

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/bhandras/delight/cli/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRefreshAccessToken_ReauthFlowWritesAccessKey(t *testing.T) {
	t.Parallel()

	var (
		mu         sync.Mutex
		challenges = make(map[string][]byte)
		nextID     = 1
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/challenge":
			var req struct {
				PublicKey string `json:"publicKey"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.NotEmpty(t, req.PublicKey)

			chal := []byte("challenge-bytes")

			mu.Lock()
			id := nextID
			nextID++
			challenges[jsonID(id)] = chal
			mu.Unlock()

			resp := map[string]any{
				"challengeId": jsonID(id),
				"challenge":   base64.StdEncoding.EncodeToString(chal),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return

		case "/v1/auth":
			var req struct {
				PublicKey   string `json:"publicKey"`
				ChallengeID string `json:"challengeId"`
				Signature   string `json:"signature"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			require.NotEmpty(t, req.PublicKey)
			require.NotEmpty(t, req.ChallengeID)
			require.NotEmpty(t, req.Signature)

			pubBytes, err := base64.StdEncoding.DecodeString(req.PublicKey)
			require.NoError(t, err)
			sig, err := base64.StdEncoding.DecodeString(req.Signature)
			require.NoError(t, err)

			mu.Lock()
			chal := challenges[req.ChallengeID]
			mu.Unlock()
			require.NotEmpty(t, chal)

			require.True(t, ed25519.Verify(ed25519.PublicKey(pubBytes), chal, sig))

			resp := map[string]any{
				"success": true,
				"token":   "tok-123",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	cfg := &config.Config{
		ServerURL:   srv.URL,
		DelightHome: home,
		AccessKey:   filepath.Join(home, "access.key"),
	}

	masterSecret := make([]byte, 32)
	for i := range masterSecret {
		masterSecret[i] = byte(i + 1)
	}

	token, err := RefreshAccessToken(cfg, masterSecret)
	require.NoError(t, err)
	require.Equal(t, "tok-123", token)

	written, err := os.ReadFile(cfg.AccessKey)
	require.NoError(t, err)
	require.Equal(t, "tok-123", string(written))
}

// jsonID formats a stable challenge id string for the test server state.
func jsonID(id int) string {
	return "c" + strconv.Itoa(id)
}
