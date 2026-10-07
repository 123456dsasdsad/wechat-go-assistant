package weixin

import (
	"bytes"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

func encryptECB(plain, key []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, errors.New("AES-128 requires a 16-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	out := make([]byte, len(plain)+padding)
	copy(out, plain)
	copy(out[len(plain):], bytes.Repeat([]byte{byte(padding)}, padding))
	for start := 0; start < len(out); start += aes.BlockSize {
		block.Encrypt(out[start:start+aes.BlockSize], out[start:start+aes.BlockSize])
	}
	return out, nil
}

func unpadPKCS7(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("invalid padded media length")
	}
	n := int(data[len(data)-1])
	if n < 1 || n > aes.BlockSize {
		return nil, errors.New("invalid media padding")
	}
	for _, b := range data[len(data)-n:] {
		if int(b) != n {
			return nil, errors.New("invalid media padding")
		}
	}
	return data[:len(data)-n], nil
}

func decryptECB(cipher, key []byte) ([]byte, error) {
	if len(key) != 16 || len(cipher) == 0 || len(cipher)%aes.BlockSize != 0 {
		return nil, errors.New("invalid media key or ciphertext length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(cipher))
	for start := 0; start < len(out); start += aes.BlockSize {
		block.Decrypt(out[start:start+aes.BlockSize], cipher[start:start+aes.BlockSize])
	}
	return unpadPKCS7(out)
}

func decodeMediaKey(encoded string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid media key encoding")
	}
	if len(decoded) == 16 {
		return decoded, nil
	}
	if len(decoded) == 32 {
		key, err := hex.DecodeString(string(decoded))
		if err == nil && len(key) == 16 {
			return key, nil
		}
	}
	return nil, errors.New("media key must decode to 16 bytes or 32 hex characters")
}
