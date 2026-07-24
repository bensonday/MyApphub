package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"myapphub/server/internal/auth"
	"myapphub/server/internal/config"
	"myapphub/server/internal/mobileconfig"
	"myapphub/server/internal/model"
	"myapphub/server/internal/store"
)

const (
	maxMetadataBytes = 1 << 20
	maxIconBytes     = 2 << 20
	maxSmallBody     = 64 << 10
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	logger   *slog.Logger
	mux      *http.ServeMux
	signer   *mobileconfig.Signer
	password *limiter
	upload   *limiter
	udid     *limiter
	udidMu   sync.Mutex
}

type accessRequest struct {
	Password string         `json:"password"`
	Platform model.Platform `json:"platform,omitempty"`
}
type accessResponse struct {
	DownloadURL string    `json:"downloadUrl,omitempty"`
	InstallURL  string    `json:"installUrl,omitempty"`
	ExpiresAt   time.Time `json:"expiresAt"`
}
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type udidSession struct {
	Challenge   string    `json:"challenge"`
	PayloadUUID string    `json:"payloadUuid"`
	CreatedAt   time.Time `json:"createdAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	UDID        string    `json:"udid,omitempty"`
	Used        bool      `json:"used"`
}

func New(cfg config.Config, repository *store.Store, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	var signer *mobileconfig.Signer
	var err error
	if cfg.ProfileCertificate != "" || cfg.ProfilePrivateKey != "" {
		signer, err = mobileconfig.LoadSigner(cfg.ProfileCertificate, cfg.ProfilePrivateKey)
		if err != nil {
			return nil, fmt.Errorf("load profile signer: %w", err)
		}
	}
	s := &Server{cfg: cfg, store: repository, logger: logger, mux: http.NewServeMux(), signer: signer,
		password: newLimiter(5, 10*time.Minute), upload: newLimiter(20, time.Hour), udid: newLimiter(10, 10*time.Minute)}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.security(s.cors(s.mux)) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /api/v1/apps", s.admin(s.listApps))
	s.mux.HandleFunc("POST /api/v1/packages", s.admin(s.uploadPackage))
	s.mux.HandleFunc("DELETE /api/v1/apps/{slug}", s.admin(s.deleteApp))
	s.mux.HandleFunc("POST /api/v1/apps/{slug}/versions/{version}/promote", s.admin(s.promote))
	s.mux.HandleFunc("DELETE /api/v1/apps/{slug}/versions/{version}", s.admin(s.deleteVersion))
	s.mux.HandleFunc("POST /api/v1/apps/{slug}/versions/{version}/restore", s.admin(s.restoreVersion))
	s.mux.HandleFunc("PUT /api/v1/apps/{slug}/password", s.admin(s.setPassword))
	s.mux.HandleFunc("GET /api/public/config", s.publicConfig)
	s.mux.HandleFunc("GET /api/public/apps/{slug}", s.publicApp)
	s.mux.HandleFunc("POST /api/public/apps/{slug}/access", s.access)
	s.mux.HandleFunc("GET /download/{slug}/{version}", s.download)
	s.mux.HandleFunc("HEAD /download/{slug}/{version}", s.download)
	s.mux.HandleFunc("GET /download/{slug}/{platform}/{version}", s.download)
	s.mux.HandleFunc("HEAD /download/{slug}/{platform}/{version}", s.download)
	s.mux.HandleFunc("GET /manifest/{slug}/{version}", s.manifest)
	s.mux.HandleFunc("GET /manifest/{slug}/{platform}/{version}", s.manifest)
	s.mux.HandleFunc("GET /assets/{slug}/{name}", s.icon)
	s.mux.HandleFunc("GET /assets/{slug}/{platform}/{name}", s.icon)
	s.mux.HandleFunc("POST /api/udid/sessions", s.createUDIDSession)
	s.mux.HandleFunc("GET /api/udid/profile/{challenge}", s.udidProfile)
	s.mux.HandleFunc("PUT /api/udid/callback", s.udidCallback)
	s.mux.HandleFunc("POST /api/udid/callback", s.udidCallback)
	s.mux.HandleFunc("GET /api/udid/result/{challenge}", s.udidResult)
	s.mux.HandleFunc("DELETE /api/udid/result/{challenge}", s.deleteUDIDResult)
	s.mux.HandleFunc("/", s.notFound)
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if !s.cfg.AllowHTTP {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		protocol := "http"
		if r.TLS != nil {
			protocol = "https"
		}
		if s.cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") != "" {
			protocol = strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
		}
		if !s.cfg.AllowHTTP && protocol != "https" {
			writeError(w, http.StatusBadRequest, "https_required", "HTTPS is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "Not found")
}

func (s *Server) publicConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]string{"projectName": s.cfg.ProjectName})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !s.originAllowed(origin) {
			writeError(w, http.StatusForbidden, "origin_not_allowed", "Origin not allowed")
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After, Content-Length, Content-Range")
		w.Header().Add("Vary", "Origin")
		if r.Method != http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		method := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
		if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost && method != http.MethodDelete {
			writeError(w, http.StatusForbidden, "cors_method_not_allowed", "CORS method not allowed")
			return
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			header = strings.ToLower(strings.TrimSpace(header))
			if header != "" && header != "accept" && header != "content-type" {
				writeError(w, http.StatusForbidden, "cors_header_not_allowed", "CORS header not allowed")
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		w.WriteHeader(http.StatusNoContent)
	})
}

func (s *Server) originAllowed(origin string) bool {
	publicURL, err := url.Parse(s.cfg.PublicURL)
	if err == nil && origin == publicURL.Scheme+"://"+publicURL.Host {
		return true
	}
	for _, allowed := range s.cfg.CORSOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if len(provided) != len(s.cfg.AdminToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.AdminToken)) != 1 {
			s.logger.Warn("admin token rejected", "ip", clientIP(r, s.cfg.TrustProxy))
			writeError(w, http.StatusUnauthorized, "unauthorized", "Unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	free, _ := s.store.AvailableBytes()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "apps": len(s.store.Apps()), "diskAvailableBytes": free})
}

func (s *Server) listApps(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"apps": s.store.Apps()})
}

func (s *Server) uploadPackage(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.cfg.TrustProxy)
	if allowed, retryAfter := s.upload.Allow(ip); !allowed {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many uploads")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxPackageBytes+maxMetadataBytes+maxIconBytes+1<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Expected multipart/form-data")
		return
	}
	staging, err := s.store.NewStagingFile()
	if err != nil {
		s.internal(w, err)
		return
	}
	stagingPath := staging.Name()
	_ = staging.Close()
	defer os.Remove(stagingPath)
	var metadata model.UploadMetadata
	var haveMetadata, havePackage bool
	var icon []byte
	var size int64
	hash := sha256.New()
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_upload", "Invalid upload body")
			return
		}
		switch part.FormName() {
		case "metadata":
			data, readErr := io.ReadAll(io.LimitReader(part, maxMetadataBytes+1))
			if readErr != nil || len(data) > maxMetadataBytes || json.Unmarshal(data, &metadata) != nil {
				writeError(w, http.StatusBadRequest, "invalid_metadata", "Invalid metadata")
				return
			}
			haveMetadata = true
		case "icon":
			data, readErr := io.ReadAll(io.LimitReader(part, maxIconBytes+1))
			if readErr != nil || len(data) > maxIconBytes {
				writeError(w, http.StatusBadRequest, "invalid_icon", "Invalid icon")
				return
			}
			icon = data
		case "package":
			if !haveMetadata {
				writeError(w, http.StatusBadRequest, "invalid_upload", "Metadata must precede package")
				return
			}
			free, freeErr := s.store.AvailableBytes()
			if freeErr != nil {
				s.internal(w, freeErr)
				return
			}
			if metadata.FileSize <= 0 || uint64(metadata.FileSize*2+s.cfg.DiskReserveBytes) > free {
				writeError(w, http.StatusInsufficientStorage, "insufficient_storage", "Not enough disk space")
				return
			}
			file, openErr := os.OpenFile(stagingPath, os.O_WRONLY|os.O_TRUNC, 0o600)
			if openErr != nil {
				s.internal(w, openErr)
				return
			}
			size, err = io.Copy(io.MultiWriter(file, hash), io.LimitReader(part, s.cfg.MaxPackageBytes+1))
			syncErr := file.Sync()
			closeErr := file.Close()
			if err != nil || syncErr != nil || closeErr != nil || size > s.cfg.MaxPackageBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "package_too_large", "Package is too large or incomplete")
				return
			}
			havePackage = true
		}
		_ = part.Close()
	}
	if !haveMetadata || !havePackage {
		writeError(w, http.StatusBadRequest, "invalid_upload", "Metadata and package are required")
		return
	}
	file, err := os.Open(stagingPath)
	if err != nil {
		s.internal(w, err)
		return
	}
	magic := make([]byte, 4)
	_, err = io.ReadFull(file, magic)
	_ = file.Close()
	if err != nil || len(magic) < 3 || magic[0] != 'P' || magic[1] != 'K' || (magic[2] != 3 && magic[2] != 5 && magic[2] != 7) {
		writeError(w, http.StatusBadRequest, "invalid_package", "Package is not a ZIP-based IPA/APK")
		return
	}
	result, err := s.store.CommitUpload(stagingPath, metadata, size, hex.EncodeToString(hash.Sum(nil)), icon)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.logger.Info("package uploaded", "ip", ip, "slug", result.App.Slug, "version", result.Version.ID, "duplicate", result.Duplicate)
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}

func (s *Server) promote(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.Promote(r.PathValue("slug"), r.PathValue("version"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.logger.Info("version promoted", "slug", app.Slug, "version", app.CurrentVersionID)
	writeJSON(w, http.StatusOK, map[string]any{"app": app})
}
func (s *Server) deleteVersion(w http.ResponseWriter, r *http.Request) {
	until, err := s.store.DeleteVersion(r.PathValue("slug"), r.PathValue("version"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.logger.Info("version deleted", "slug", r.PathValue("slug"), "version", r.PathValue("version"))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "recoverableUntil": until})
}
func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) {
	until, err := s.store.DeleteApp(r.PathValue("slug"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.logger.Info("app deleted", "slug", r.PathValue("slug"))
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "recoverableUntil": until})
}
func (s *Server) restoreVersion(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.RestoreVersion(r.PathValue("slug"), r.PathValue("version"))
	if err != nil {
		s.storeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": app})
}
func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var body accessRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	app, err := s.store.SetPassword(r.PathValue("slug"), body.Password)
	if err != nil {
		s.storeError(w, err)
		return
	}
	s.logger.Info("share password changed", "slug", app.Slug)
	writeJSON(w, http.StatusOK, map[string]any{"app": app})
}

func (s *Server) publicApp(w http.ResponseWriter, r *http.Request) {
	apps := s.store.AppsForSlug(r.PathValue("slug"))
	if len(apps) == 0 {
		s.storeError(w, store.ErrNotFound)
		return
	}
	releases := make([]model.PublicRelease, 0, len(apps))
	for _, candidate := range apps {
		if candidate.CurrentVersionID == "" {
			continue
		}
		version := candidate.Versions[candidate.CurrentVersionID]
		version.PackagePath = ""
		releases = append(releases, model.PublicRelease{App: candidate.Public(false), Version: version})
	}
	if len(releases) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "no_current_version", "message": "No installable version", "app": apps[0].Public(false)})
		return
	}
	selected := releases[0]
	requested := model.Platform(r.URL.Query().Get("platform"))
	for _, release := range releases {
		if release.App.Platform == requested {
			selected = release
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": selected.App, "version": selected.Version, "variants": releases})
}

func (s *Server) access(w http.ResponseWriter, r *http.Request) {
	var body accessRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	var app model.App
	var err error
	if body.Platform == "" {
		app, err = s.store.App(r.PathValue("slug"))
	} else {
		app, err = s.store.AppPlatform(r.PathValue("slug"), body.Platform)
	}
	if err != nil {
		s.storeError(w, err)
		return
	}
	if app.CurrentVersionID == "" {
		writeError(w, http.StatusServiceUnavailable, "no_current_version", "No installable version")
		return
	}
	key := clientIP(r, s.cfg.TrustProxy) + ":" + app.Slug
	if app.SharePasswordHash != "" {
		if allowed, retryAfter := s.password.Allow(key); !allowed {
			setRetryAfter(w, retryAfter)
			writeError(w, http.StatusTooManyRequests, "rate_limited", "Try again later")
			return
		}
		if !auth.VerifyPassword(body.Password, app.SharePasswordHash) {
			s.logger.Warn("share password rejected", "ip", clientIP(r, s.cfg.TrustProxy), "slug", app.Slug)
			writeError(w, http.StatusUnauthorized, "invalid_password", "Invalid password")
			return
		}
		s.password.Reset(key)
	}
	version := app.Versions[app.CurrentVersionID]
	expires := time.Now().Add(s.cfg.InstallTokenTTL)
	downloadToken, _ := auth.Sign(s.cfg.HMACSecret, auth.Claims{Purpose: "download", Slug: app.Slug, VersionID: version.ID, PasswordVersion: app.PasswordVersion}, s.cfg.InstallTokenTTL)
	result := accessResponse{DownloadURL: fmt.Sprintf("%s/download/%s/%s/%s?token=%s", s.cfg.PublicURL, url.PathEscape(app.Slug), app.Platform, url.PathEscape(version.ID), url.QueryEscape(downloadToken)), ExpiresAt: expires}
	if app.Platform == model.PlatformIOS {
		manifestToken, _ := auth.Sign(s.cfg.HMACSecret, auth.Claims{Purpose: "manifest", Slug: app.Slug, VersionID: version.ID, PasswordVersion: app.PasswordVersion}, s.cfg.InstallTokenTTL)
		manifestURL := fmt.Sprintf("%s/manifest/%s/%s/%s?token=%s", s.cfg.PublicURL, url.PathEscape(app.Slug), app.Platform, url.PathEscape(version.ID), url.QueryEscape(manifestToken))
		result.InstallURL = "itms-services://?action=download-manifest&url=" + url.QueryEscape(manifestURL)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	app, err := s.appForVersionRequest(r)
	if err != nil {
		s.storeError(w, err)
		return
	}
	version, ok := app.Versions[r.PathValue("version")]
	if !ok {
		s.storeError(w, store.ErrNotFound)
		return
	}
	if _, err := auth.Verify(s.cfg.HMACSecret, r.URL.Query().Get("token"), auth.Claims{Purpose: "download", Slug: app.Slug, VersionID: version.ID, PasswordVersion: app.PasswordVersion}); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "Invalid or expired token")
		return
	}
	file, err := os.Open(version.PackagePath)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer file.Close()
	contentType := "application/vnd.android.package-archive"
	if app.Platform == model.PlatformIOS {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s%s"`, app.Slug, version.VersionName, filepath.Ext(version.PackageFile)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", `"`+version.SHA256+`"`)
	http.ServeContent(w, r, version.PackageFile, version.UploadedAt, file)
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	app, err := s.appForVersionRequest(r)
	if err != nil {
		s.storeError(w, err)
		return
	}
	version, ok := app.Versions[r.PathValue("version")]
	if !ok || app.Platform != model.PlatformIOS {
		s.storeError(w, store.ErrNotFound)
		return
	}
	if _, err := auth.Verify(s.cfg.HMACSecret, r.URL.Query().Get("token"), auth.Claims{Purpose: "manifest", Slug: app.Slug, VersionID: version.ID, PasswordVersion: app.PasswordVersion}); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_token", "Invalid or expired token")
		return
	}
	downloadToken, _ := auth.Sign(s.cfg.HMACSecret, auth.Claims{Purpose: "download", Slug: app.Slug, VersionID: version.ID, PasswordVersion: app.PasswordVersion}, s.cfg.InstallTokenTTL)
	downloadURL := fmt.Sprintf("%s/download/%s/%s/%s?token=%s", s.cfg.PublicURL, url.PathEscape(app.Slug), app.Platform, url.PathEscape(version.ID), url.QueryEscape(downloadToken))
	body := []byte(manifestXML(app, version, downloadURL))
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) icon(w http.ResponseWriter, r *http.Request) {
	var app model.App
	var err error
	if platform := model.Platform(r.PathValue("platform")); platform != "" {
		app, err = s.store.AppPlatform(r.PathValue("slug"), platform)
	} else {
		app, err = s.store.App(r.PathValue("slug"))
	}
	if err != nil || app.Icon == "" || r.PathValue("name") != app.Icon {
		s.storeError(w, store.ErrNotFound)
		return
	}
	path := filepath.Join(app.StoragePath, app.Icon)
	file, err := os.Open(path)
	if err != nil {
		s.internal(w, err)
		return
	}
	defer file.Close()
	info, _ := file.Stat()
	if strings.HasSuffix(app.Icon, ".png") {
		w.Header().Set("Content-Type", "image/png")
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, app.Icon, info.ModTime(), file)
}

func (s *Server) appForVersionRequest(r *http.Request) (model.App, error) {
	if platform := model.Platform(r.PathValue("platform")); platform != "" {
		return s.store.AppPlatform(r.PathValue("slug"), platform)
	}
	return s.store.AppForVersion(r.PathValue("slug"), r.PathValue("version"))
}

func (s *Server) createUDIDSession(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		writeError(w, http.StatusServiceUnavailable, "udid_not_configured", "UDID profile signing is not configured")
		return
	}
	if allowed, retryAfter := s.udid.Allow(clientIP(r, s.cfg.TrustProxy)); !allowed {
		setRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Try again later")
		return
	}
	challengeBytes := make([]byte, 32)
	_, _ = rand.Read(challengeBytes)
	now := time.Now().UTC()
	session := udidSession{Challenge: base64.RawURLEncoding.EncodeToString(challengeBytes), PayloadUUID: randomUUID(), CreatedAt: now, ExpiresAt: now.Add(s.cfg.UDIDTTL)}
	if err := writeJSONFile(filepath.Join(s.store.UDIDDir(), session.Challenge+".json"), session); err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"challenge": session.Challenge, "profileUrl": "/api/udid/profile/" + session.Challenge, "resultUrl": "/api/udid/result/" + session.Challenge})
}

func (s *Server) udidProfile(w http.ResponseWriter, r *http.Request) {
	session, err := s.readUDIDSession(r.PathValue("challenge"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_expired", "Session expired")
		return
	}
	profile := profileXML(session, s.cfg.PublicURL+"/api/udid/callback", s.cfg.ProjectName)
	signed, err := s.signer.Sign([]byte(profile))
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="apphub-udid.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(signed)
}

func (s *Server) udidCallback(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSmallBody+1))
	if err != nil || len(data) > maxSmallBody {
		writeError(w, http.StatusBadRequest, "invalid_profile", "Invalid profile response")
		return
	}
	values, err := parsePlistStrings(data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_profile", "Invalid profile response")
		return
	}
	challenge, udid := values["CHALLENGE"], values["UDID"]
	if store.ValidateSegment(challenge) != nil || !validUDID(udid) {
		writeError(w, http.StatusBadRequest, "invalid_profile", "Invalid profile response")
		return
	}
	s.udidMu.Lock()
	defer s.udidMu.Unlock()
	session, err := s.readUDIDSession(challenge)
	if err != nil || session.Used {
		writeError(w, http.StatusConflict, "invalid_challenge", "Session expired or already used")
		return
	}
	session.UDID = udid
	session.Used = true
	if err := writeJSONFile(filepath.Join(s.store.UDIDDir(), challenge+".json"), session); err != nil {
		s.internal(w, err)
		return
	}
	s.logger.Info("udid completed", "ip", clientIP(r, s.cfg.TrustProxy))
	completed := finalProfileXML(randomUUID(), s.cfg.ProjectName)
	signed, err := s.signer.Sign([]byte(completed))
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(signed)
}

func (s *Server) udidResult(w http.ResponseWriter, r *http.Request) {
	session, err := s.readUDIDSession(r.PathValue("challenge"))
	if err != nil {
		writeError(w, http.StatusNotFound, "session_expired", "Session expired")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"pending": session.UDID == "", "udid": session.UDID})
}
func (s *Server) deleteUDIDResult(w http.ResponseWriter, r *http.Request) {
	challenge := r.PathValue("challenge")
	if store.ValidateSegment(challenge) != nil {
		writeError(w, http.StatusBadRequest, "invalid_challenge", "Invalid challenge")
		return
	}
	_ = os.Remove(filepath.Join(s.store.UDIDDir(), challenge+".json"))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) readUDIDSession(challenge string) (udidSession, error) {
	if store.ValidateSegment(challenge) != nil {
		return udidSession{}, errors.New("invalid challenge")
	}
	var session udidSession
	data, err := os.ReadFile(filepath.Join(s.store.UDIDDir(), challenge+".json"))
	if err != nil {
		return session, err
	}
	if json.Unmarshal(data, &session) != nil || time.Now().After(session.ExpiresAt) {
		return udidSession{}, errors.New("expired")
	}
	return session, nil
}

func (s *Server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "version_conflict", err.Error())
	case errors.Is(err, store.ErrCurrentVersion):
		writeError(w, http.StatusConflict, "current_version", err.Error())
	case errors.Is(err, store.ErrWriteBusy):
		writeError(w, http.StatusConflict, "write_busy", err.Error())
	default:
		if strings.Contains(err.Error(), "invalid") || strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "mismatch") {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		} else {
			s.internal(w, err)
		}
	}
}
func (s *Server) internal(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err.Error())
	writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Expected application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSmallBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy && r.Header.Get("X-Forwarded-For") != "" {
		return strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func manifestXML(app model.App, version model.Version, downloadURL string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>items</key><array><dict><key>assets</key><array><dict><key>kind</key><string>software-package</string><key>url</key><string>%s</string></dict></array><key>metadata</key><dict><key>bundle-identifier</key><string>%s</string><key>bundle-version</key><string>%s</string><key>kind</key><string>software</string><key>title</key><string>%s</string></dict></dict></array></dict></plist>`, html.EscapeString(downloadURL), html.EscapeString(app.Identifier), html.EscapeString(version.VersionName), html.EscapeString(app.Name))
}
func profileXML(session udidSession, callback, projectName string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>PayloadContent</key><dict><key>URL</key><string>%s</string><key>DeviceAttributes</key><array><string>UDID</string><string>VERSION</string><string>PRODUCT</string></array><key>Challenge</key><string>%s</string></dict><key>PayloadOrganization</key><string>%s</string><key>PayloadDisplayName</key><string>获取设备 UDID</string><key>PayloadDescription</key><string>仅用于显示并复制本设备 UDID，结果将在 30 分钟后删除。</string><key>PayloadVersion</key><integer>1</integer><key>PayloadUUID</key><string>%s</string><key>PayloadIdentifier</key><string>com.myapphub.udid.%s</string><key>PayloadType</key><string>Profile Service</string></dict></plist>`, html.EscapeString(callback), html.EscapeString(session.Challenge), html.EscapeString(projectName), session.PayloadUUID, session.PayloadUUID)
}
func finalProfileXML(uuid, projectName string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>PayloadContent</key><array/><key>PayloadDisplayName</key><string>UDID 获取完成</string><key>PayloadIdentifier</key><string>com.myapphub.complete.%s</string><key>PayloadOrganization</key><string>%s</string><key>PayloadType</key><string>Configuration</string><key>PayloadUUID</key><string>%s</string><key>PayloadVersion</key><integer>1</integer></dict></plist>`, uuid, html.EscapeString(projectName), uuid)
}

func parsePlistStrings(data []byte) (map[string]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	result := make(map[string]string)
	key := ""
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "key":
			if err := decoder.DecodeElement(&key, &start); err != nil {
				return nil, err
			}
		case "string":
			var value string
			if err := decoder.DecodeElement(&value, &start); err != nil {
				return nil, err
			}
			if key != "" {
				result[key] = strings.TrimSpace(value)
				key = ""
			}
		}
	}
	return result, nil
}
func validUDID(value string) bool {
	if len(value) < 20 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f' || char >= 'A' && char <= 'F' || char == '-') {
			return false
		}
	}
	return true
}
func randomUUID() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	text := hex.EncodeToString(bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", text[:8], text[8:12], text[12:16], text[16:20], text[20:])
}
func writeJSONFile(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if chmodErr := temp.Chmod(0o600); chmodErr != nil {
		temp.Close()
		return chmodErr
	}
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

type limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]limitEntry
}
type limitEntry struct {
	Count int
	Reset time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, entries: make(map[string]limitEntry)}
}
func (l *limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	entry := l.entries[key]
	if now.After(entry.Reset) {
		entry = limitEntry{Reset: now.Add(l.window)}
	}
	entry.Count++
	l.entries[key] = entry
	if entry.Count <= l.limit {
		return true, 0
	}
	return false, time.Until(entry.Reset)
}
func (l *limiter) Reset(key string) { l.mu.Lock(); delete(l.entries, key); l.mu.Unlock() }

func setRetryAfter(w http.ResponseWriter, duration time.Duration) {
	seconds := int64((duration + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
}

func Shutdown(ctx context.Context, server *http.Server) error { return server.Shutdown(ctx) }

var _ = multipart.ErrMessageTooLarge
