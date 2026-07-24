package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func validEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("APPHUB_ROOT", t.TempDir())
	t.Setenv("APPHUB_TOKEN", strings.Repeat("a", 32))
	t.Setenv("APPHUB_HMAC_SECRET", strings.Repeat("b", 32))
	t.Setenv("APPHUB_PUBLIC_URL", "https://hub.example.com/")
	t.Setenv("APPHUB_ALLOW_HTTP", "false")
	t.Setenv("APPHUB_PROJECT_NAME", "")
}

func TestLoadDiscoversProfileSignerNextToServerData(t *testing.T) {
	validEnvironment(t)
	root := os.Getenv("APPHUB_ROOT")
	certificate := filepath.Join(root, "profile-cert.pem")
	privateKey := filepath.Join(root, "profile-key.pem")
	if err := os.WriteFile(certificate, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKey, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProfileCertificate != certificate || cfg.ProfilePrivateKey != privateKey {
		t.Fatalf("profile signer was not discovered: %+v", cfg)
	}
}

func TestLoadDefaultsAndOverrides(t *testing.T) {
	validEnvironment(t)
	t.Setenv("APPHUB_MAX_PACKAGE_BYTES", "12345")
	t.Setenv("APPHUB_UPLOAD_TIMEOUT", "45s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://hub.example.com" || cfg.ProjectName != "MyApphub" || cfg.MaxPackageBytes != 12345 || cfg.UploadTimeout != 45*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if !filepath.IsAbs(cfg.DataDir) || !filepath.IsAbs(cfg.RuntimeDir) {
		t.Fatal("paths must be absolute")
	}
	if cfg.DataDir != filepath.Join(cfg.RootDir, "data") || cfg.RuntimeDir != filepath.Join(cfg.RootDir, "runtime") || cfg.LogFile != filepath.Join(cfg.RootDir, "runtime", "apphub.log") {
		t.Fatalf("default server paths are not rooted under APPHUB_ROOT: %+v", cfg)
	}
}

func TestLoadProjectNameOverrideAndWhitespaceFallback(t *testing.T) {
	validEnvironment(t)
	t.Setenv("APPHUB_PROJECT_NAME", "  团队应用中心  ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectName != "团队应用中心" {
		t.Fatalf("project name was not normalized: %q", cfg.ProjectName)
	}

	t.Setenv("APPHUB_PROJECT_NAME", " \t ")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectName != "MyApphub" {
		t.Fatalf("blank project name did not use default: %q", cfg.ProjectName)
	}
}

func TestLoadResolvesRelativeServerPathsFromRoot(t *testing.T) {
	validEnvironment(t)
	t.Setenv("APPHUB_DATA_DIR", "storage")
	t.Setenv("APPHUB_RUNTIME_DIR", "var")
	t.Setenv("APPHUB_LOG_FILE", "var/server.log")
	t.Setenv("APPHUB_PROFILE_CERT", "secrets/profile-cert.pem")
	t.Setenv("APPHUB_PROFILE_KEY", "secrets/profile-key.pem")
	t.Setenv("APPHUB_CORS_ORIGINS", "https://web.example.com, http://localhost:5173/,https://web.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != filepath.Join(cfg.RootDir, "storage") || cfg.RuntimeDir != filepath.Join(cfg.RootDir, "var") || cfg.LogFile != filepath.Join(cfg.RootDir, "var", "server.log") {
		t.Fatalf("relative server paths were not resolved from root: %+v", cfg)
	}
	if cfg.ProfileCertificate != filepath.Join(cfg.RootDir, "secrets", "profile-cert.pem") || cfg.ProfilePrivateKey != filepath.Join(cfg.RootDir, "secrets", "profile-key.pem") {
		t.Fatalf("profile paths were not resolved from root: %+v", cfg)
	}
	if strings.Join(cfg.CORSOrigins, ",") != "https://web.example.com,http://localhost:5173" {
		t.Fatalf("CORS origins were not normalized: %+v", cfg.CORSOrigins)
	}
}

func TestLoadRejectsInvalidCORSOrigin(t *testing.T) {
	validEnvironment(t)
	for _, value := range []string{"*", "https://web.example.com/path", "javascript:alert(1)"} {
		t.Setenv("APPHUB_CORS_ORIGINS", value)
		if _, err := Load(); err == nil {
			t.Fatalf("invalid CORS origin %q accepted", value)
		}
	}
}

func TestLoadRejectsWeakSecretsAndHTTP(t *testing.T) {
	validEnvironment(t)
	t.Setenv("APPHUB_TOKEN", "weak")
	if _, err := Load(); err == nil {
		t.Fatal("weak token accepted")
	}
	validEnvironment(t)
	t.Setenv("APPHUB_PUBLIC_URL", "http://example.com")
	if _, err := Load(); err == nil {
		t.Fatal("plain HTTP accepted")
	}
	t.Setenv("APPHUB_ALLOW_HTTP", "true")
	if _, err := Load(); err != nil {
		t.Fatalf("explicit HTTP development mode rejected: %v", err)
	}
}

func TestLoadReturnsInvalidNumberError(t *testing.T) {
	validEnvironment(t)
	t.Setenv("APPHUB_MAX_PACKAGE_BYTES", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("invalid number did not return an error")
	}
}
