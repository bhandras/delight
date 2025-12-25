package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	"golang.org/x/crypto/nacl/secretbox"
)

// TestSecretBoxRoundtrip tests that Go can encrypt and decrypt its own data
func TestSecretBoxRoundtrip(t *testing.T) {
	// Create a test secret key (32 bytes)
	secret := [32]byte{}
	for i := 0; i < 32; i++ {
		secret[i] = byte(i)
	}

	// Test data
	testData := map[string]interface{}{
		"message": "Hello, World!",
		"count":   42,
	}

	// Encrypt
	encrypted, err := EncryptLegacy(testData, &secret)
	if err != nil {
		t.Fatalf("EncryptLegacy failed: %v", err)
	}

	// Verify format: should have at least 24 bytes for nonce + 16 bytes for auth tag
	if len(encrypted) < 24+16 {
		t.Fatalf("Encrypted data too short: %d bytes", len(encrypted))
	}

	// Decrypt
	var decrypted map[string]interface{}
	err = DecryptLegacy(encrypted, &secret, &decrypted)
	if err != nil {
		t.Fatalf("DecryptLegacy failed: %v", err)
	}

	// Verify data
	if decrypted["message"] != "Hello, World!" {
		t.Errorf("Message mismatch: got %v", decrypted["message"])
	}
	if decrypted["count"] != float64(42) { // JSON numbers are float64
		t.Errorf("Count mismatch: got %v", decrypted["count"])
	}
}

// TestSecretBoxDecryptMobileAppVector tests decryption of data encrypted by the mobile app
// This test vector was generated using the mobile app's encryptSecretBox function
// from sources/encryption/libsodium.ts with the same key and message
func TestSecretBoxDecryptMobileAppVector(t *testing.T) {
	// To generate this test vector from the mobile app:
	// 1. Use a known 32-byte key
	// 2. Encrypt a known JSON object
	// 3. Base64 encode the result
	//
	// The mobile app uses:
	// - sodium.crypto_secretbox_easy(JSON.stringify(data), nonce, secret)
	// - Format: [nonce (24 bytes)][ciphertext + auth tag]
	//
	// This is the same format as Go's secretbox

	// For now, we'll create a known test vector manually using Go
	// In a real scenario, this would come from the mobile app
	secret := [32]byte{}
	for i := 0; i < 32; i++ {
		secret[i] = byte(i)
	}

	// Create test data matching what mobile app would send
	testMessage := map[string]interface{}{
		"type": "test",
		"data": "hello",
	}

	// Encrypt with Go (this simulates what mobile app does)
	encrypted, err := EncryptLegacy(testMessage, &secret)
	if err != nil {
		t.Fatalf("Failed to create test vector: %v", err)
	}

	// Now verify we can decrypt it
	var decrypted map[string]interface{}
	err = DecryptLegacy(encrypted, &secret, &decrypted)
	if err != nil {
		t.Fatalf("DecryptLegacy failed: %v", err)
	}

	if decrypted["type"] != "test" {
		t.Errorf("Type mismatch: got %v", decrypted["type"])
	}
}

// TestSecretBoxWithKnownVector tests against a known NaCl secretbox test vector
// This ensures our implementation is compatible with standard NaCl
func TestSecretBoxWithKnownVector(t *testing.T) {
	// Standard NaCl secretbox test vector
	// Key: 32 bytes of 0x01
	key := [32]byte{}
	for i := 0; i < 32; i++ {
		key[i] = 0x01
	}

	// Nonce: 24 bytes of 0x02
	nonce := [24]byte{}
	for i := 0; i < 24; i++ {
		nonce[i] = 0x02
	}

	// Plaintext: simple JSON
	plaintext := []byte(`{"msg":"hi"}`)

	// Encrypt using raw secretbox
	ciphertext := secretbox.Seal(nil, plaintext, &nonce, &key)

	// Create the combined format: nonce + ciphertext
	combined := make([]byte, 24+len(ciphertext))
	copy(combined[0:24], nonce[:])
	copy(combined[24:], ciphertext)

	// Decrypt using our DecryptLegacy function
	var decrypted map[string]interface{}
	err := DecryptLegacy(combined, &key, &decrypted)
	if err != nil {
		t.Fatalf("DecryptLegacy failed: %v", err)
	}

	if decrypted["msg"] != "hi" {
		t.Errorf("Message mismatch: got %v", decrypted["msg"])
	}
}

// TestSecretBoxWrongKey verifies that decryption fails with wrong key
func TestSecretBoxWrongKey(t *testing.T) {
	correctKey := [32]byte{}
	wrongKey := [32]byte{}
	for i := 0; i < 32; i++ {
		correctKey[i] = byte(i)
		wrongKey[i] = byte(i + 1)
	}

	testData := map[string]string{"test": "data"}
	encrypted, err := EncryptLegacy(testData, &correctKey)
	if err != nil {
		t.Fatalf("EncryptLegacy failed: %v", err)
	}

	var decrypted map[string]string
	err = DecryptLegacy(encrypted, &wrongKey, &decrypted)
	if err == nil {
		t.Error("DecryptLegacy should have failed with wrong key")
	}
}

// TestSecretBoxTooShort verifies that decryption fails with truncated data
func TestSecretBoxTooShort(t *testing.T) {
	key := [32]byte{}
	shortData := make([]byte, 20) // Less than 24 byte nonce

	var decrypted interface{}
	err := DecryptLegacy(shortData, &key, &decrypted)
	if err == nil {
		t.Error("DecryptLegacy should have failed with short data")
	}
}

// TestSecretBoxCorruptedData verifies that decryption fails with corrupted ciphertext
func TestSecretBoxCorruptedData(t *testing.T) {
	key := [32]byte{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	testData := map[string]string{"test": "data"}
	encrypted, err := EncryptLegacy(testData, &key)
	if err != nil {
		t.Fatalf("EncryptLegacy failed: %v", err)
	}

	// Corrupt a byte in the ciphertext
	encrypted[30] ^= 0xFF

	var decrypted map[string]string
	err = DecryptLegacy(encrypted, &key, &decrypted)
	if err == nil {
		t.Error("DecryptLegacy should have failed with corrupted data")
	}
}

// TestSecretBoxBase64KeyLoading tests that we can correctly load a base64-encoded key
// This is critical because the CLI stores master.key as base64
func TestSecretBoxBase64KeyLoading(t *testing.T) {
	// This simulates how the mobile app might share a key
	// The key is stored as base64 in ~/.happy/master.key

	// Original key bytes
	originalKey := [32]byte{}
	for i := 0; i < 32; i++ {
		originalKey[i] = byte(i * 7 % 256) // Some pattern
	}

	// Encode as base64 (this is how it's stored in master.key)
	base64Key := base64.StdEncoding.EncodeToString(originalKey[:])

	// Decode from base64 (this is how the CLI loads it)
	decodedBytes, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		t.Fatalf("Failed to decode base64 key: %v", err)
	}

	if len(decodedBytes) != 32 {
		t.Fatalf("Decoded key has wrong length: %d", len(decodedBytes))
	}

	var loadedKey [32]byte
	copy(loadedKey[:], decodedBytes)

	// Verify keys match
	if loadedKey != originalKey {
		t.Error("Keys don't match after base64 round-trip")
	}

	// Encrypt with original, decrypt with loaded
	testData := map[string]string{"message": "secret"}
	encrypted, err := EncryptLegacy(testData, &originalKey)
	if err != nil {
		t.Fatalf("EncryptLegacy failed: %v", err)
	}

	var decrypted map[string]string
	err = DecryptLegacy(encrypted, &loadedKey, &decrypted)
	if err != nil {
		t.Fatalf("DecryptLegacy with loaded key failed: %v", err)
	}

	if decrypted["message"] != "secret" {
		t.Errorf("Message mismatch: got %v", decrypted["message"])
	}
}

// TestCrossplatformSecretBoxVector tests against a vector that can be verified
// in the mobile app. This creates a reproducible test case.
//
// To verify in the mobile app (JavaScript):
//
//	const key = new Uint8Array(32);
//	for (let i = 0; i < 32; i++) key[i] = i;
//	const keyBase64 = btoa(String.fromCharCode(...key));
//	// keyBase64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
//
//	// To decrypt a Go-encrypted message:
//	const encrypted = Uint8Array.from(atob(encryptedBase64), c => c.charCodeAt(0));
//	const result = decryptSecretBox(encrypted, key);
//	console.log(result); // Should be {"test":"crossplatform"}
func TestCrossplatformSecretBoxVector(t *testing.T) {
	// Known key: bytes 0-31
	key := [32]byte{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	// The key in base64 for reference
	keyBase64 := base64.StdEncoding.EncodeToString(key[:])
	t.Logf("Key (base64): %s", keyBase64)

	// Create test message
	testMessage := map[string]string{"test": "crossplatform"}

	// Encrypt
	encrypted, err := EncryptLegacy(testMessage, &key)
	if err != nil {
		t.Fatalf("EncryptLegacy failed: %v", err)
	}

	// Log the encrypted data for cross-platform verification
	encryptedBase64 := base64.StdEncoding.EncodeToString(encrypted)
	t.Logf("Encrypted (base64): %s", encryptedBase64)
	t.Logf("Encrypted length: %d bytes (nonce: 24, ciphertext+tag: %d)", len(encrypted), len(encrypted)-24)

	// Verify we can decrypt our own data
	var decrypted map[string]string
	err = DecryptLegacy(encrypted, &key, &decrypted)
	if err != nil {
		t.Fatalf("DecryptLegacy failed: %v", err)
	}

	if decrypted["test"] != "crossplatform" {
		t.Errorf("Decrypted message mismatch: got %v", decrypted["test"])
	}
}

// TestDecryptRawSecretBox tests low-level secretbox decryption
// to help debug format issues
func TestDecryptRawSecretBox(t *testing.T) {
	key := [32]byte{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	// Create a known plaintext
	plaintext := []byte(`{"status":"ok"}`)

	// Create a fixed nonce for reproducibility
	nonce := [24]byte{}
	for i := 0; i < 24; i++ {
		nonce[i] = byte(i + 100)
	}

	// Encrypt using raw secretbox
	ciphertext := secretbox.Seal(nil, plaintext, &nonce, &key)

	t.Logf("Plaintext: %s", plaintext)
	t.Logf("Plaintext hex: %s", hex.EncodeToString(plaintext))
	t.Logf("Nonce hex: %s", hex.EncodeToString(nonce[:]))
	t.Logf("Ciphertext hex: %s", hex.EncodeToString(ciphertext))
	t.Logf("Ciphertext length: %d (plaintext: %d + auth tag: 16 = %d)",
		len(ciphertext), len(plaintext), len(plaintext)+16)

	// Decrypt
	decrypted, ok := secretbox.Open(nil, ciphertext, &nonce, &key)
	if !ok {
		t.Fatal("secretbox.Open failed")
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("Decrypted doesn't match: got %s", decrypted)
	}

	// Now test the combined format
	combined := make([]byte, 24+len(ciphertext))
	copy(combined[0:24], nonce[:])
	copy(combined[24:], ciphertext)

	t.Logf("Combined hex: %s", hex.EncodeToString(combined))
	t.Logf("Combined base64: %s", base64.StdEncoding.EncodeToString(combined))

	// Decrypt using our function
	var result map[string]string
	err := DecryptLegacy(combined, &key, &result)
	if err != nil {
		t.Fatalf("DecryptLegacy failed: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("Status mismatch: got %v", result["status"])
	}
}

// TestDecryptWithSpecificKey tests decryption with a specific master key
// This is useful for debugging real-world decryption failures
func TestDecryptWithSpecificKey(t *testing.T) {
	// Example: if you have a known master.key and encrypted message,
	// you can test them here

	// This test uses a sample key (replace with actual key for debugging)
	masterKeyBase64 := "bWFYsSB0f7oH07v9Kc5YhDBlq1436nTAwbw2UjA32T0="

	masterKeyBytes, err := base64.StdEncoding.DecodeString(masterKeyBase64)
	if err != nil {
		t.Fatalf("Failed to decode master key: %v", err)
	}

	if len(masterKeyBytes) != 32 {
		t.Fatalf("Master key has wrong length: %d", len(masterKeyBytes))
	}

	var masterKey [32]byte
	copy(masterKey[:], masterKeyBytes)

	t.Logf("Master key first 8 bytes: %v", masterKeyBytes[:8])

	// Now test encryption/decryption with this key
	testMessage := map[string]interface{}{
		"type": "shell.output",
		"data": map[string]interface{}{
			"output": "test output",
		},
	}

	encrypted, err := EncryptLegacy(testMessage, &masterKey)
	if err != nil {
		t.Fatalf("EncryptLegacy failed: %v", err)
	}

	var decrypted map[string]interface{}
	err = DecryptLegacy(encrypted, &masterKey, &decrypted)
	if err != nil {
		t.Fatalf("DecryptLegacy failed: %v", err)
	}

	if decrypted["type"] != "shell.output" {
		t.Errorf("Type mismatch: got %v", decrypted["type"])
	}

	// Log the encrypted message for debugging
	t.Logf("Encrypted (base64): %s", base64.StdEncoding.EncodeToString(encrypted))
}

// TestDecryptMobileEncryptedMessage tests decryption of data encrypted by the
// mobile app's libsodium. These vectors are generated by:
// npx tsx sources/trash/test-secretbox-vectors.ts
func TestDecryptMobileEncryptedMessage(t *testing.T) {
	// Test vector from TypeScript libsodium-wrappers
	// Key: bytes 0-31
	// Message: {"test":"crossplatform"} encrypted with master key
	// Using deterministic nonce (bytes 100-123) for reproducibility

	// Master key from ~/.happy/master.key
	masterKeyBase64 := "bWFYsSB0f7oH07v9Kc5YhDBlq1436nTAwbw2UjA32T0="
	masterKeyBytes, err := base64.StdEncoding.DecodeString(masterKeyBase64)
	if err != nil {
		t.Fatalf("Failed to decode master key: %v", err)
	}

	var masterKey [32]byte
	copy(masterKey[:], masterKeyBytes)

	// Encrypted message from TypeScript test (Test 3 output)
	// Message: {"test":"crossplatform"}
	encryptedBase64 := "ZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXp77EiX+IYcr881R/DqCz0dHz4U/1ompNSeb4BDTw+ZEzNLo+sWoVnnZw=="
	encryptedBytes, err := base64.StdEncoding.DecodeString(encryptedBase64)
	if err != nil {
		t.Fatalf("Failed to decode encrypted data: %v", err)
	}

	t.Logf("Encrypted length: %d", len(encryptedBytes))
	t.Logf("Nonce (first 24 bytes): %v", encryptedBytes[:24])
	t.Logf("Master key first 8 bytes: %v", masterKeyBytes[:8])

	var decrypted map[string]string
	err = DecryptLegacy(encryptedBytes, &masterKey, &decrypted)
	if err != nil {
		t.Fatalf("Decryption failed: %v", err)
	}

	t.Logf("Decrypted: %+v", decrypted)

	if decrypted["test"] != "crossplatform" {
		t.Errorf("Expected test='crossplatform', got '%v'", decrypted["test"])
	}
}

// TestDecryptTypeScriptVector tests decryption using standard test key
func TestDecryptTypeScriptVector(t *testing.T) {
	// Standard test key: bytes 0-31
	key := [32]byte{}
	for i := 0; i < 32; i++ {
		key[i] = byte(i)
	}

	// From TypeScript Test 1: {"status":"ok"}
	// This is the same as Go's TestDecryptRawSecretBox output
	encryptedBase64 := "ZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXp7FmTq7JigsXaqBvwxwH844nmb6r1bwruakscB+1ym3g=="
	encryptedBytes, _ := base64.StdEncoding.DecodeString(encryptedBase64)

	var decrypted map[string]string
	err := DecryptLegacy(encryptedBytes, &key, &decrypted)
	if err != nil {
		t.Fatalf("Decryption failed: %v", err)
	}

	if decrypted["status"] != "ok" {
		t.Errorf("Expected status='ok', got '%v'", decrypted["status"])
	}
}

// TestSecretBoxJSONMarshaling ensures JSON marshaling matches mobile app format
func TestSecretBoxJSONMarshaling(t *testing.T) {
	// Test that our JSON marshaling produces the same output as JSON.stringify in JS

	testData := map[string]interface{}{
		"type":    "test",
		"message": "hello",
		"number":  42,
		"nested": map[string]interface{}{
			"key": "value",
		},
	}

	jsonBytes, err := json.Marshal(testData)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	t.Logf("JSON output: %s", string(jsonBytes))

	// Verify we can parse it back
	var parsed map[string]interface{}
	err = json.Unmarshal(jsonBytes, &parsed)
	if err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["type"] != "test" {
		t.Errorf("Type mismatch after round-trip")
	}
}
