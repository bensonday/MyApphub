package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword("correct horse battery staple", hash) {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("wrong password accepted")
	}
	second, _ := HashPassword("correct horse battery staple")
	if second == hash {
		t.Fatal("hashes must use independent salts")
	}
}

func TestPasswordValidation(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if VerifyPassword("password", "not-a-hash") {
		t.Fatal("malformed hash accepted")
	}
}

func TestSignedTokenValidation(t *testing.T) {
	secret := strings.Repeat("s", 32)
	token, err := Sign(secret, Claims{Purpose: "download", Slug: "demo", VersionID: "v1", PasswordVersion: 2}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := Verify(secret, token, Claims{Purpose: "download", Slug: "demo", VersionID: "v1", PasswordVersion: 2})
	if err != nil || claims.Slug != "demo" {
		t.Fatalf("valid token rejected: %v", err)
	}
	for name, expected := range map[string]Claims{
		"purpose":          {Purpose: "manifest", Slug: "demo", VersionID: "v1", PasswordVersion: 2},
		"app":              {Purpose: "download", Slug: "other", VersionID: "v1", PasswordVersion: 2},
		"version":          {Purpose: "download", Slug: "demo", VersionID: "v2", PasswordVersion: 2},
		"password version": {Purpose: "download", Slug: "demo", VersionID: "v1", PasswordVersion: 3},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(secret, token, expected); err == nil {
				t.Fatal("mismatched token accepted")
			}
		})
	}
	if _, err := Verify(secret, token+"x", Claims{Purpose: "download", Slug: "demo", VersionID: "v1", PasswordVersion: 2}); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestExpiredToken(t *testing.T) {
	token, _ := Sign(strings.Repeat("x", 32), Claims{Purpose: "download", Slug: "demo"}, -time.Second)
	if _, err := Verify(strings.Repeat("x", 32), token, Claims{Purpose: "download", Slug: "demo"}); err == nil {
		t.Fatal("expired token accepted")
	}
}

func TestConstantEqual(t *testing.T) {
	if !Equal("same", "same") || Equal("same", "other") || Equal("short", "longer") {
		t.Fatal("constant comparison returned wrong result")
	}
}
