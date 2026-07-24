package mobileconfig

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testSignerFiles(t *testing.T) (string, string) {
	return testSignerFilesWithValidity(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
}

func testSignerFilesWithValidity(t *testing.T, notBefore, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "MyApphub Tests"}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestCMSSignedProfileCanBeVerified(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is unavailable")
	}
	certPath, keyPath := testSignerFiles(t)
	signer, err := LoadSigner(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`<?xml version="1.0"?><plist><dict><key>Test</key><string>value</string></dict></plist>`)
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "profile.der"), filepath.Join(dir, "verified.plist")
	if err := os.WriteFile(input, signed, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("openssl", "cms", "-verify", "-inform", "DER", "-in", input, "-noverify", "-out", output)
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("OpenSSL rejected CMS: %v\n%s", err, result)
	}
	verified, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(verified, payload) {
		t.Fatalf("verified payload differs:\n%s", verified)
	}
}

func TestLoadSignerValidation(t *testing.T) {
	if _, err := LoadSigner("", ""); err == nil {
		t.Fatal("missing files accepted")
	}
	certPath, _ := testSignerFiles(t)
	_, otherKey := testSignerFiles(t)
	if _, err := LoadSigner(certPath, otherKey); err == nil {
		t.Fatal("mismatched key accepted")
	}
	expiredCert, expiredKey := testSignerFilesWithValidity(t, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if _, err := LoadSigner(expiredCert, expiredKey); err == nil {
		t.Fatal("expired certificate accepted")
	}
}
