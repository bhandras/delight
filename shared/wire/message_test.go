package wire

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractMessageCipher(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		want    string
		wantID  string
		wantOK  bool
		wantErr bool
	}{
		{
			name: "string message field",
			input: map[string]any{
				"sid":     "s1",
				"message": "cipher",
			},
			want:   "cipher",
			wantOK: true,
		},
		{
			name: "message.content string",
			input: map[string]any{
				"sid": "s1",
				"message": map[string]any{
					"content": "cipher",
				},
			},
			want:   "cipher",
			wantOK: true,
		},
		{
			name: "message.content object with c",
			input: map[string]any{
				"sid": "s1",
				"message": map[string]any{
					"content": map[string]any{
						"t": "encrypted",
						"c": "cipher",
					},
				},
			},
			want:   "cipher",
			wantOK: true,
		},
		{
			name: "content legacy string",
			input: map[string]any{
				"sid":     "s1",
				"content": "cipher",
			},
			want:   "cipher",
			wantOK: true,
		},
		{
			name: "content legacy object with c",
			input: map[string]any{
				"sid": "s1",
				"content": map[string]any{
					"c": "cipher",
				},
			},
			want:   "cipher",
			wantOK: true,
		},
		{
			name: "localId extracted",
			input: map[string]any{
				"sid":     "s1",
				"localId": "lid-1",
				"message": "cipher",
			},
			want:   "cipher",
			wantID: "lid-1",
			wantOK: true,
		},
		{
			name: "missing cipher yields ok=false",
			input: map[string]any{
				"sid": "s1",
			},
			wantOK: false,
		},
		{
			name: "unsupported cipher envelope yields error",
			input: map[string]any{
				"sid": "s1",
				"message": map[string]any{
					"nope": "x",
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCipher, gotID, ok, err := ExtractMessageCipher(tc.input)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.want, gotCipher)
			require.Equal(t, tc.wantID, gotID)
		})
	}
}

func TestParseUserTextRecord(t *testing.T) {
	text, meta, ok, err := ParseUserTextRecord([]byte(`{
  "role":"user",
  "content": { "type":"text", "text":"hello" },
  "meta": { "model": "gpt-5" }
}`))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "hello", text)
	require.Equal(t, map[string]any{"model": "gpt-5"}, meta)

	_, _, ok, err = ParseUserTextRecord([]byte(`{
  "role":"assistant",
  "content": { "type":"text", "text":"hi" }
}`))
	require.NoError(t, err)
	require.False(t, ok)
}
