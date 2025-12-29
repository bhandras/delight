//go:build !ios && !android

package sdk

// KeyPair holds a base64-encoded keypair.
//
// Note: This type intentionally does not exist in gomobile builds; use
// `KeyPairBuffers` instead.
type KeyPair struct {
	publicKey  string
	privateKey string
}

// PublicKey returns the base64-encoded public key.
func (k *KeyPair) PublicKey() string {
	return k.publicKey
}

// PrivateKey returns the base64-encoded private key.
func (k *KeyPair) PrivateKey() string {
	return k.privateKey
}

// GenerateEd25519KeyPair creates a new signing keypair for /v1/auth.
func GenerateEd25519KeyPair() (*KeyPair, error) {
	pub, priv, err := generateEd25519KeyPairBase64()
	if err != nil {
		return nil, err
	}
	return &KeyPair{
		publicKey:  pub,
		privateKey: priv,
	}, nil
}

