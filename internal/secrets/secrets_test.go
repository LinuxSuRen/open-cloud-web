package secrets

import "testing"

func TestEncryptDecrypt(t *testing.T) {
	enc, err := Encrypt("my-cloud-secret", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "my-cloud-secret" {
		t.Fatal("未加密")
	}
	got, err := Decrypt(enc, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-cloud-secret" {
		t.Fatalf("got %q", got)
	}
	if _, err := Decrypt(enc, "wrong-key"); err == nil {
		t.Fatal("错误密钥应解密失败")
	}
}

func TestEncryptNonDeterministic(t *testing.T) {
	a, _ := Encrypt("x", "k")
	b, _ := Encrypt("x", "k")
	if a == b {
		t.Fatal("随机 nonce 应使密文每次不同")
	}
}
