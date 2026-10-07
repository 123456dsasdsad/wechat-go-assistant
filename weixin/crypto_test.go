package weixin

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestAESKnownVectorPaddingAndKeyFormats(t *testing.T) {
	key, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	plain, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	cipher, err := encryptECB(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(cipher[:16]) != "69c4e0d86a7b0430d8cdb78070b4c55a" || len(cipher) != 32 {
		t.Fatalf("not compatible AES ECB/PKCS7: %x", cipher)
	}
	for _, n := range []int{0, 1, 15, 16, 17, 1024} {
		input := bytes.Repeat([]byte{0x42}, n)
		enc, err := encryptECB(input, key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decryptECB(enc, key)
		if err != nil || !bytes.Equal(got, input) {
			t.Fatalf("roundtrip %d: %v", n, err)
		}
	}
	for _, encoded := range []string{base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString([]byte(hex.EncodeToString(key)))} {
		got, err := decodeMediaKey(encoded)
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("key format failed: %v", err)
		}
	}
	if _, err := decodeMediaKey("not-a-key"); err == nil {
		t.Fatal("invalid key accepted")
	}
	if _, err := decryptECB([]byte{1}, key); err == nil {
		t.Fatal("invalid ciphertext accepted")
	}
	bad := bytes.Repeat([]byte{0}, 16)
	if _, err := unpadPKCS7(bad); err == nil {
		t.Fatal("zero padding accepted")
	}
	bad[15] = 2
	bad[14] = 1
	if _, err := unpadPKCS7(bad); err == nil {
		t.Fatal("inconsistent padding accepted")
	}
}
