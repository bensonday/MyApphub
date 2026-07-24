package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	RootDir            string
	DataDir            string
	RuntimeDir         string
	Address            string
	PublicURL          string
	ProjectName        string
	AdminToken         string
	HMACSecret         string
	AllowHTTP          bool
	TrustProxy         bool
	MaxPackageBytes    int64
	DiskReserveBytes   int64
	UploadTimeout      time.Duration
	InstallTokenTTL    time.Duration
	UDIDTTL            time.Duration
	TrashRetention     time.Duration
	ProfileCertificate string
	ProfilePrivateKey  string
	LogFile            string
	CORSOrigins        []string
}

func Load() (Config, error) {
	root, err := filepath.Abs(value("APPHUB_ROOT", "."))
	if err != nil {
		return Config{}, err
	}
	profileCertificate, profilePrivateKey := profilePaths(root)
	cfg := Config{
		RootDir:     root,
		DataDir:     resolve(root, value("APPHUB_DATA_DIR", "data")),
		RuntimeDir:  resolve(root, value("APPHUB_RUNTIME_DIR", "runtime")),
		Address:     value("APPHUB_ADDRESS", "127.0.0.1:8787"),
		PublicURL:   strings.TrimRight(value("APPHUB_PUBLIC_URL", "http://127.0.0.1:8787"), "/"),
		ProjectName: projectName(os.Getenv("APPHUB_PROJECT_NAME")),
		AdminToken:  os.Getenv("APPHUB_TOKEN"), HMACSecret: os.Getenv("APPHUB_HMAC_SECRET"),
		AllowHTTP: boolean("APPHUB_ALLOW_HTTP", false), TrustProxy: boolean("APPHUB_TRUST_PROXY", true),
		MaxPackageBytes: 1 << 30, DiskReserveBytes: 10 << 30,
		UploadTimeout: 30 * time.Minute, InstallTokenTTL: 2 * time.Hour,
		UDIDTTL: 30 * time.Minute, TrashRetention: 7 * 24 * time.Hour,
		ProfileCertificate: profileCertificate,
		ProfilePrivateKey:  profilePrivateKey,
		LogFile:            resolve(root, value("APPHUB_LOG_FILE", filepath.Join("runtime", "apphub.log"))),
	}
	if cfg.CORSOrigins, err = origins(os.Getenv("APPHUB_CORS_ORIGINS")); err != nil {
		return Config{}, err
	}
	if cfg.MaxPackageBytes, err = integer("APPHUB_MAX_PACKAGE_BYTES", cfg.MaxPackageBytes); err != nil {
		return Config{}, err
	}
	if cfg.DiskReserveBytes, err = integer("APPHUB_DISK_RESERVE_BYTES", cfg.DiskReserveBytes); err != nil {
		return Config{}, err
	}
	if cfg.UploadTimeout, err = duration("APPHUB_UPLOAD_TIMEOUT", cfg.UploadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.InstallTokenTTL, err = duration("APPHUB_INSTALL_TOKEN_TTL", cfg.InstallTokenTTL); err != nil {
		return Config{}, err
	}
	if cfg.UDIDTTL, err = duration("APPHUB_UDID_TTL", cfg.UDIDTTL); err != nil {
		return Config{}, err
	}
	if cfg.TrashRetention, err = duration("APPHUB_TRASH_RETENTION", cfg.TrashRetention); err != nil {
		return Config{}, err
	}
	if len(cfg.AdminToken) < 32 {
		return Config{}, errors.New("APPHUB_TOKEN must contain at least 32 bytes")
	}
	if len(cfg.HMACSecret) < 32 {
		return Config{}, errors.New("APPHUB_HMAC_SECRET must contain at least 32 bytes")
	}
	if !cfg.AllowHTTP && !strings.HasPrefix(cfg.PublicURL, "https://") {
		return Config{}, errors.New("APPHUB_PUBLIC_URL must use HTTPS unless APPHUB_ALLOW_HTTP=true")
	}
	if cfg.MaxPackageBytes < 4 {
		return Config{}, errors.New("APPHUB_MAX_PACKAGE_BYTES is too small")
	}
	return cfg, nil
}

func projectName(raw string) string {
	if name := strings.TrimSpace(raw); name != "" {
		return name
	}
	return "MyApphub"
}

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func resolve(root, path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	result, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return result
}
func optionalPath(root, path string) string {
	if path == "" {
		return ""
	}
	return resolve(root, path)
}
func profilePaths(root string) (string, string) {
	certificate := os.Getenv("APPHUB_PROFILE_CERT")
	privateKey := os.Getenv("APPHUB_PROFILE_KEY")
	if certificate != "" || privateKey != "" {
		return optionalPath(root, certificate), optionalPath(root, privateKey)
	}
	certificate = filepath.Join(root, "profile-cert.pem")
	privateKey = filepath.Join(root, "profile-key.pem")
	_, certificateError := os.Stat(certificate)
	_, privateKeyError := os.Stat(privateKey)
	if certificateError == nil || privateKeyError == nil {
		return certificate, privateKey
	}
	return "", ""
}
func origins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	result := make([]string, 0)
	seen := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimRight(strings.TrimSpace(item), "/")
		parsed, err := url.Parse(item)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return nil, fmt.Errorf("APPHUB_CORS_ORIGINS contains invalid origin %q", item)
		}
		if _, exists := seen[item]; !exists {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result, nil
}
func boolean(name string, fallback bool) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	return err == nil && value
}
func integer(name string, fallback int64) (int64, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return value, nil
}
func duration(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	return value, nil
}
