package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bhandras/delight/cli/internal/config"
	"github.com/bhandras/delight/cli/internal/crypto"
	"github.com/bhandras/delight/cli/internal/storage"
	"github.com/bhandras/delight/cli/internal/websocket"
)

func StopDaemonCommand(cfg *config.Config) error {
	tokenData, err := os.ReadFile(cfg.AccessKey)
	if err != nil {
		return fmt.Errorf("failed to read access token: %w", err)
	}
	token := string(tokenData)

	machineIDPath := filepath.Join(cfg.DelightHome, "machine.id")
	machineIDBytes, err := os.ReadFile(machineIDPath)
	if err != nil {
		return fmt.Errorf("failed to read machine id: %w", err)
	}
	machineID := string(machineIDBytes)
	if machineID == "" {
		return fmt.Errorf("machine id is empty")
	}

	masterSecret, err := storage.LoadSecretKey(filepath.Join(cfg.DelightHome, "master.key"))
	if err != nil {
		return fmt.Errorf("failed to load master key: %w", err)
	}

	client := websocket.NewUserClient(cfg.ServerURL, token, cfg.SocketIOTransport, cfg.Debug)
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	if !client.WaitForConnect(5 * time.Second) {
		return fmt.Errorf("timeout connecting to server")
	}

	params, err := encryptMachineParams(masterSecret, map[string]interface{}{})
	if err != nil {
		return err
	}

	resp, err := client.EmitWithAck("rpc-call", map[string]interface{}{
		"method": fmt.Sprintf("%s:stop-daemon", machineID),
		"params": params,
	}, 10*time.Second)
	if err != nil {
		return err
	}
	if ok, _ := resp["ok"].(bool); !ok {
		if errMsg, _ := resp["error"].(string); errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
		return fmt.Errorf("stop-daemon failed")
	}

	result, err := decryptMachineResult(masterSecret, resp["result"])
	if err != nil {
		return err
	}
	if msg, ok := result["message"].(string); ok && msg != "" {
		fmt.Println(msg)
	}
	return nil
}

func encryptMachineParams(masterSecret []byte, payload map[string]interface{}) (string, error) {
	if len(masterSecret) != 32 {
		return "", fmt.Errorf("master secret must be 32 bytes, got %d", len(masterSecret))
	}
	encrypted, err := crypto.EncryptWithDataKey(payload, masterSecret)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func decryptMachineResult(masterSecret []byte, raw any) (map[string]interface{}, error) {
	var encoded string
	switch v := raw.(type) {
	case string:
		encoded = v
	case []byte:
		encoded = string(v)
	default:
		if m, ok := raw.(map[string]interface{}); ok {
			return m, nil
		}
		return nil, fmt.Errorf("unexpected rpc result type %T", raw)
	}

	encrypted, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}

	var out map[string]interface{}
	if len(masterSecret) != 32 {
		return nil, fmt.Errorf("master secret must be 32 bytes, got %d", len(masterSecret))
	}
	if err := crypto.DecryptWithDataKey(encrypted, masterSecret, &out); err != nil {
		// Some handlers may return plain JSON; try to decode raw string.
		if err := json.Unmarshal([]byte(encoded), &out); err == nil {
			return out, nil
		}
		return nil, err
	}
	return out, nil
}
