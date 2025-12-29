package sdk

// KeyPairBuffers holds a base64-encoded keypair as Buffers (gomobile-safe).
//
// IMPORTANT: Do not add methods returning string/[]byte here; return Buffers.
type KeyPairBuffers struct {
	publicKey  *Buffer
	privateKey *Buffer
}

func newKeyPairBuffers(publicKeyB64, privateKeyB64 string) *KeyPairBuffers {
	return &KeyPairBuffers{
		publicKey:  newBufferFromString(publicKeyB64),
		privateKey: newBufferFromString(privateKeyB64),
	}
}

// PublicKey returns the base64-encoded public key as a Buffer.
func (k *KeyPairBuffers) PublicKey() *Buffer {
	if k == nil {
		return nil
	}
	return k.publicKey
}

// PrivateKey returns the base64-encoded private key as a Buffer.
func (k *KeyPairBuffers) PrivateKey() *Buffer {
	if k == nil {
		return nil
	}
	return k.privateKey
}

