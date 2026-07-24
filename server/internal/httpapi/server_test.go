package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"myapphub/server/internal/config"
	"myapphub/server/internal/model"
	"myapphub/server/internal/store"
)

type testEnvironment struct {
	cfg        config.Config
	repository *store.Store
	server     *httptest.Server
	apiClient  *testAPIClient
	logs       *bytes.Buffer
}

type testAPIClient struct {
	baseURL string
	token   string
}

func (c *testAPIClient) request(ctx context.Context, method, requestPath string, body io.Reader, contentType string, target any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+requestPath, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var value struct{ Message string }
		_ = json.NewDecoder(response.Body).Decode(&value)
		if value.Message == "" {
			value.Message = response.Status
		}
		return fmt.Errorf("%s", value.Message)
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func (c *testAPIClient) Upload(ctx context.Context, packagePath string, metadata model.UploadMetadata, _ []byte, _ io.Writer) (model.UploadResult, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataPart, err := writer.CreateFormField("metadata")
	if err != nil {
		return model.UploadResult{}, err
	}
	if err := json.NewEncoder(metadataPart).Encode(metadata); err != nil {
		return model.UploadResult{}, err
	}
	packagePart, err := writer.CreateFormFile("package", filepath.Base(packagePath))
	if err != nil {
		return model.UploadResult{}, err
	}
	data, err := os.ReadFile(packagePath)
	if err != nil {
		return model.UploadResult{}, err
	}
	if _, err := packagePart.Write(data); err != nil {
		return model.UploadResult{}, err
	}
	if err := writer.Close(); err != nil {
		return model.UploadResult{}, err
	}
	var result model.UploadResult
	err = c.request(ctx, http.MethodPost, "/api/v1/packages", &body, writer.FormDataContentType(), &result)
	return result, err
}

func (c *testAPIClient) List(ctx context.Context) ([]model.PublicApp, error) {
	var result struct {
		Apps []model.PublicApp `json:"apps"`
	}
	err := c.request(ctx, http.MethodGet, "/api/v1/apps", nil, "", &result)
	return result.Apps, err
}

func (c *testAPIClient) SetPassword(ctx context.Context, slug, password string) (model.PublicApp, error) {
	data, err := json.Marshal(map[string]string{"password": password})
	if err != nil {
		return model.PublicApp{}, err
	}
	var result struct {
		App model.PublicApp `json:"app"`
	}
	err = c.request(ctx, http.MethodPut, "/api/v1/apps/"+url.PathEscape(slug)+"/password", bytes.NewReader(data), "application/json", &result)
	return result.App, err
}

func newTestEnvironment(t *testing.T, withSigner bool) *testEnvironment {
	t.Helper()
	root := t.TempDir()
	logs := &bytes.Buffer{}
	cfg := config.Config{RootDir: root, DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "runtime"), PublicURL: "http://example.invalid", ProjectName: "MyApphub", AdminToken: strings.Repeat("a", 32), HMACSecret: strings.Repeat("b", 32), AllowHTTP: true, TrustProxy: true, MaxPackageBytes: 1 << 20, DiskReserveBytes: 0, UploadTimeout: time.Minute, InstallTokenTTL: time.Hour, UDIDTTL: 30 * time.Minute, TrashRetention: 7 * 24 * time.Hour}
	if withSigner {
		cfg.ProfileCertificate, cfg.ProfilePrivateKey = makeCertificate(t, root)
	}
	repository := store.New(cfg)
	if err := repository.Init(); err != nil {
		t.Fatal(err)
	}
	api, err := New(cfg, repository, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	apiClient := &testAPIClient{baseURL: server.URL, token: cfg.AdminToken}
	t.Cleanup(server.Close)
	return &testEnvironment{cfg: cfg, repository: repository, server: server, apiClient: apiClient, logs: logs}
}

func makeCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "App Hub Test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "profile-cert.pem"), filepath.Join(dir, "profile-key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func packageMetadata(platform model.Platform, identifier, version, build string, content []byte) model.UploadMetadata {
	hash := sha256.Sum256(content)
	value := model.UploadMetadata{Platform: platform, Identifier: identifier, Name: "Demo App", VersionName: version, BuildNumber: build, SHA256: hex.EncodeToString(hash[:]), FileSize: int64(len(content)), Promote: true}
	if platform == model.PlatformIOS {
		value.DistributionType = "ad-hoc"
	}
	return value
}

func upload(t *testing.T, env *testEnvironment, metadata model.UploadMetadata, content []byte) model.UploadResult {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package")
	if metadata.Platform == model.PlatformIOS {
		path += ".ipa"
	} else {
		path += ".apk"
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := env.apiClient.Upload(context.Background(), path, metadata, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requestJSON(t *testing.T, method, target string, body any, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var input io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		input = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, target, input)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.NewDecoder(response.Body).Decode(&value)
	_ = response.Body.Close()
	return response, value
}

func localize(env *testEnvironment, remote string) string {
	parsed, _ := url.Parse(remote)
	return env.server.URL + parsed.RequestURI()
}

func TestAdminUploadListPublicAccessAndRangeDownload(t *testing.T) {
	env := newTestEnvironment(t, false)
	response, _ := requestJSON(t, http.MethodGet, env.server.URL+"/api/v1/apps", nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized admin status = %d", response.StatusCode)
	}
	content := []byte("PK\x03\x04android-package-content")
	uploaded := upload(t, env, packageMetadata(model.PlatformAndroid, "com.example.android", "1.2.3", "12", content), content)
	apps, err := env.apiClient.List(context.Background())
	if err != nil || len(apps) != 1 || apps[0].CurrentVersionID != uploaded.Version.ID {
		t.Fatalf("list failed: %v %+v", err, apps)
	}
	response, public := requestJSON(t, http.MethodGet, env.server.URL+"/api/public/apps/"+uploaded.App.Slug, nil, nil)
	if response.StatusCode != http.StatusOK || public["version"] == nil {
		t.Fatalf("public app failed: %d %+v", response.StatusCode, public)
	}
	response, access := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/"+uploaded.App.Slug+"/access", map[string]string{"password": ""}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("access failed: %d %+v", response.StatusCode, access)
	}
	downloadURL := access["downloadUrl"].(string)
	request, _ := http.NewRequest(http.MethodGet, localize(env, downloadURL), nil)
	request.Header.Set("Range", "bytes=0-1")
	ranged, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(ranged.Body)
	ranged.Body.Close()
	if ranged.StatusCode != http.StatusPartialContent || string(data) != "PK" || ranged.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("range failed: %d %q %+v", ranged.StatusCode, data, ranged.Header)
	}
	if !strings.Contains(env.logs.String(), "package uploaded") {
		t.Fatal("upload audit event missing")
	}
}

func TestPublicConfigReturnsConfiguredProjectName(t *testing.T) {
	env := newTestEnvironment(t, false)
	env.cfg.ProjectName = "团队 <应用> & Hub"
	api, err := New(env.cfg, env.repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	response, value := requestJSON(t, http.MethodGet, server.URL+"/api/public/config", nil, nil)
	if response.StatusCode != http.StatusOK || value["projectName"] != env.cfg.ProjectName {
		t.Fatalf("public config: %d %+v", response.StatusCode, value)
	}
	if response.Header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("unexpected cache policy: %q", response.Header.Get("Cache-Control"))
	}
}

func TestProfileXMLUsesEscapedProjectNameAndNeutralIdentifier(t *testing.T) {
	session := udidSession{Challenge: "challenge", PayloadUUID: "uuid"}
	profile := profileXML(session, "https://example.com/callback?a=1&b=2", "团队 <应用> & Hub")
	completed := finalProfileXML("uuid", "团队 <应用> & Hub")
	for _, value := range []string{profile, completed} {
		if !strings.Contains(value, "团队 &lt;应用&gt; &amp; Hub") {
			t.Fatalf("project name was not safely branded: %s", value)
		}
	}
	if !strings.Contains(profile, "com.myapphub.udid.uuid") || !strings.Contains(completed, "com.myapphub.complete.uuid") {
		t.Fatalf("neutral payload identifiers missing: %s %s", profile, completed)
	}
}

func TestPublicSlugCarriesIOSAndAndroidVariants(t *testing.T) {
	env := newTestEnvironment(t, false)
	androidContent := []byte("PK\x03\x04android-package")
	android := packageMetadata(model.PlatformAndroid, "com.example.mobileapp", "1.0", "1", androidContent)
	android.Slug = "letsenglish"
	upload(t, env, android, androidContent)
	iosContent := []byte("PK\x03\x04ios-package")
	ios := packageMetadata(model.PlatformIOS, "com.example.mobileapp", "1.0", "1", iosContent)
	ios.Slug = "letsenglish"
	upload(t, env, ios, iosContent)

	response, public := requestJSON(t, http.MethodGet, env.server.URL+"/api/public/apps/letsenglish?platform=android", nil, nil)
	variants, _ := public["variants"].([]any)
	selected, _ := public["app"].(map[string]any)
	if response.StatusCode != http.StatusOK || len(variants) != 2 || selected["platform"] != "android" {
		t.Fatalf("multi-platform public response: %d %+v", response.StatusCode, public)
	}
	response, access := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/letsenglish/access", map[string]string{"platform": "android"}, nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(access["downloadUrl"].(string), "/letsenglish/android/") {
		t.Fatalf("android access: %d %+v", response.StatusCode, access)
	}
	response, access = requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/letsenglish/access", map[string]string{"platform": "ios"}, nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(access["downloadUrl"].(string), "/letsenglish/ios/") || access["installUrl"] == nil {
		t.Fatalf("ios access: %d %+v", response.StatusCode, access)
	}
}

func TestDeleteWholeAppEndpoint(t *testing.T) {
	env := newTestEnvironment(t, false)
	content := []byte("PK\x03\x04android-package")
	metadata := packageMetadata(model.PlatformAndroid, "com.example.delete-app", "1.0", "1", content)
	metadata.Slug = "delete-me"
	upload(t, env, metadata, content)
	request, _ := http.NewRequest(http.MethodDelete, env.server.URL+"/api/v1/apps/delete-me", nil)
	request.Header.Set("Authorization", "Bearer "+env.cfg.AdminToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	_ = json.NewDecoder(response.Body).Decode(&result)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || result["deleted"] != true {
		t.Fatalf("delete app: %d %+v", response.StatusCode, result)
	}
	response, _ = requestJSON(t, http.MethodGet, env.server.URL+"/api/public/apps/delete-me", nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted app remained public: %d", response.StatusCode)
	}
}

func TestPasswordRateLimitAndTokenRevocation(t *testing.T) {
	env := newTestEnvironment(t, false)
	content := []byte("PK\x03\x04protected")
	uploaded := upload(t, env, packageMetadata(model.PlatformAndroid, "com.example.protected", "1.0", "1", content), content)
	_, oldAccess := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/"+uploaded.App.Slug+"/access", map[string]string{"password": ""}, nil)
	oldDownload := oldAccess["downloadUrl"].(string)
	if _, err := env.apiClient.SetPassword(context.Background(), uploaded.App.Slug, "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	oldResponse, _ := http.Get(localize(env, oldDownload))
	oldResponse.Body.Close()
	if oldResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token survived password change: %d", oldResponse.StatusCode)
	}
	for attempt := 1; attempt <= 6; attempt++ {
		response, _ := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/"+uploaded.App.Slug+"/access", map[string]string{"password": "wrong password"}, map[string]string{"X-Forwarded-For": "10.0.0.5"})
		expected := http.StatusUnauthorized
		if attempt == 6 {
			expected = http.StatusTooManyRequests
		}
		if response.StatusCode != expected {
			t.Fatalf("attempt %d status = %d", attempt, response.StatusCode)
		}
		if attempt == 6 && response.Header.Get("Retry-After") == "" {
			t.Fatal("rate-limited response did not include Retry-After")
		}
	}
	response, access := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/"+uploaded.App.Slug+"/access", map[string]string{"password": "correct horse battery staple"}, map[string]string{"X-Forwarded-For": "10.0.0.6"})
	if response.StatusCode != http.StatusOK || access["downloadUrl"] == nil {
		t.Fatalf("correct password rejected: %d %+v", response.StatusCode, access)
	}
}

func TestPublicAppWithoutCurrentVersionIncludesPublicApp(t *testing.T) {
	env := newTestEnvironment(t, false)
	content := []byte("PK\x03\x04not-promoted")
	metadata := packageMetadata(model.PlatformAndroid, "com.example.waiting", "1.0", "1", content)
	metadata.Promote = false
	uploaded := upload(t, env, metadata, content)

	response, value := requestJSON(t, http.MethodGet, env.server.URL+"/api/public/apps/"+uploaded.App.Slug, nil, nil)
	app, ok := value["app"].(map[string]any)
	if response.StatusCode != http.StatusServiceUnavailable || value["code"] != "no_current_version" || !ok {
		t.Fatalf("unexpected response: %d %+v", response.StatusCode, value)
	}
	if app["slug"] != uploaded.App.Slug || app["name"] != uploaded.App.Name || app["platform"] != string(model.PlatformAndroid) {
		t.Fatalf("public app missing or incorrect: %+v", app)
	}
	if _, exposed := value["version"]; exposed {
		t.Fatalf("version was exposed: %+v", value)
	}
}

func TestServerDoesNotServeWebRoutes(t *testing.T) {
	env := newTestEnvironment(t, false)
	for _, path := range []string{"/", "/app/example", "/udid", "/frontend.js", "/api/does-not-exist"} {
		response, value := requestJSON(t, http.MethodGet, env.server.URL+path, nil, nil)
		if response.StatusCode != http.StatusNotFound || value["code"] != "not_found" {
			t.Fatalf("%s should be a JSON 404: %d %+v", path, response.StatusCode, value)
		}
	}
}

func TestCORSAllowsConfiguredFrontendAndRejectsOthers(t *testing.T) {
	env := newTestEnvironment(t, false)
	env.cfg.CORSOrigins = []string{"https://web.example.com"}
	api, err := New(env.cfg, env.repository, slog.New(slog.NewJSONHandler(env.logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	preflight, _ := http.NewRequest(http.MethodOptions, server.URL+"/api/udid/sessions", nil)
	preflight.Header.Set("Origin", "https://web.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	preflight.Header.Set("Access-Control-Request-Headers", "Content-Type")
	response, err := http.DefaultClient.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || response.Header.Get("Access-Control-Allow-Origin") != "https://web.example.com" || !strings.Contains(response.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("preflight response = %d %+v", response.StatusCode, response.Header)
	}

	allowed, _ := http.NewRequest(http.MethodGet, server.URL+"/api/public/apps/missing", nil)
	allowed.Header.Set("Origin", "https://web.example.com")
	response, err = http.DefaultClient.Do(allowed)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.Header.Get("Access-Control-Allow-Origin") != "https://web.example.com" || !strings.Contains(response.Header.Get("Access-Control-Expose-Headers"), "Retry-After") {
		t.Fatalf("allowed origin headers missing: %+v", response.Header)
	}

	rejected, _ := http.NewRequest(http.MethodGet, server.URL+"/healthz", nil)
	rejected.Header.Set("Origin", "https://evil.example")
	response, err = http.DefaultClient.Do(rejected)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || response.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("untrusted origin response = %d %+v", response.StatusCode, response.Header)
	}

	forbiddenHeader, _ := http.NewRequest(http.MethodOptions, server.URL+"/api/v1/apps", nil)
	forbiddenHeader.Header.Set("Origin", "https://web.example.com")
	forbiddenHeader.Header.Set("Access-Control-Request-Method", http.MethodGet)
	forbiddenHeader.Header.Set("Access-Control-Request-Headers", "Authorization")
	response, err = http.DefaultClient.Do(forbiddenHeader)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("authorization header was allowed by CORS: %d", response.StatusCode)
	}
}

func TestIOSManifestAndTokenPurposeIsolation(t *testing.T) {
	env := newTestEnvironment(t, false)
	content := []byte("PK\x03\x04ios-package")
	uploaded := upload(t, env, packageMetadata(model.PlatformIOS, "com.example.ios", "2.0", "20", content), content)
	response, access := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/"+uploaded.App.Slug+"/access", map[string]string{}, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal(access)
	}
	install, err := url.Parse(access["installUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	manifestRemote := install.Query().Get("url")
	if manifestRemote == "" {
		t.Fatal("manifest URL missing")
	}
	manifestResponse, err := http.Get(localize(env, manifestRemote))
	if err != nil {
		t.Fatal(err)
	}
	manifestData, _ := io.ReadAll(manifestResponse.Body)
	manifestResponse.Body.Close()
	if manifestResponse.StatusCode != http.StatusOK || !bytes.Contains(manifestData, []byte("com.example.ios")) || !bytes.Contains(manifestData, []byte("software-package")) {
		t.Fatalf("invalid manifest: %d %s", manifestResponse.StatusCode, manifestData)
	}
	downloadParsed, _ := url.Parse(access["downloadUrl"].(string))
	manifestParsed, _ := url.Parse(manifestRemote)
	manifestParsed.RawQuery = url.Values{"token": []string{downloadParsed.Query().Get("token")}}.Encode()
	wrong, _ := http.Get(localize(env, manifestParsed.String()))
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusUnauthorized {
		t.Fatalf("download token accepted as manifest token: %d", wrong.StatusCode)
	}
}

func TestUDIDProfileCallbackReplayAndDeletion(t *testing.T) {
	env := newTestEnvironment(t, true)
	response, created := requestJSON(t, http.MethodPost, env.server.URL+"/api/udid/sessions", nil, map[string]string{"X-Forwarded-For": "10.0.0.7"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d %+v", response.StatusCode, created)
	}
	challenge := created["challenge"].(string)
	profile, err := http.Get(env.server.URL + created["profileUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	profileData, _ := io.ReadAll(profile.Body)
	profile.Body.Close()
	if profile.StatusCode != http.StatusOK || profile.Header.Get("Content-Type") != "application/x-apple-aspen-config" || len(profileData) < 100 {
		t.Fatalf("profile failed: %d", profile.StatusCode)
	}
	udid := "00008110-0012345678901234"
	callback := fmt.Sprintf(`<plist><dict><key>UDID</key><string>%s</string><key>CHALLENGE</key><string>%s</string></dict></plist>`, udid, challenge)
	callbackRequest, _ := http.NewRequest(http.MethodPut, env.server.URL+"/api/udid/callback", strings.NewReader(callback))
	callbackResponse, err := http.DefaultClient.Do(callbackRequest)
	if err != nil {
		t.Fatal(err)
	}
	callbackResponse.Body.Close()
	if callbackResponse.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d", callbackResponse.StatusCode)
	}
	replayRequest, _ := http.NewRequest(http.MethodPut, env.server.URL+"/api/udid/callback", strings.NewReader(callback))
	replay, _ := http.DefaultClient.Do(replayRequest)
	replay.Body.Close()
	if replay.StatusCode != http.StatusConflict {
		t.Fatalf("replay status = %d", replay.StatusCode)
	}
	response, result := requestJSON(t, http.MethodGet, env.server.URL+"/api/udid/result/"+challenge, nil, nil)
	if response.StatusCode != http.StatusOK || result["udid"] != udid {
		t.Fatalf("result failed: %d %+v", response.StatusCode, result)
	}
	if strings.Contains(env.logs.String(), udid) {
		t.Fatal("full UDID leaked into logs")
	}
	response, _ = requestJSON(t, http.MethodDelete, env.server.URL+"/api/udid/result/"+challenge, nil, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatal("delete failed")
	}
	response, _ = requestJSON(t, http.MethodGet, env.server.URL+"/api/udid/result/"+challenge, nil, nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("deleted UDID result still exists")
	}
}

func TestUDIDUnavailableWithoutSigningCertificate(t *testing.T) {
	env := newTestEnvironment(t, false)
	response, value := requestJSON(t, http.MethodPost, env.server.URL+"/api/udid/sessions", nil, nil)
	if response.StatusCode != http.StatusServiceUnavailable || value["code"] != "udid_not_configured" {
		t.Fatalf("unexpected response: %d %+v", response.StatusCode, value)
	}
}

func TestUDIDRateLimitIncludesRetryAfter(t *testing.T) {
	env := newTestEnvironment(t, true)
	for attempt := 1; attempt <= 11; attempt++ {
		response, value := requestJSON(t, http.MethodPost, env.server.URL+"/api/udid/sessions", nil, map[string]string{"X-Forwarded-For": "10.0.0.88"})
		if attempt <= 10 && response.StatusCode != http.StatusCreated {
			t.Fatalf("attempt %d unexpectedly failed: %d %+v", attempt, response.StatusCode, value)
		}
		if attempt == 11 && (response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "") {
			t.Fatalf("rate limit missing Retry-After: %d %+v %+v", response.StatusCode, response.Header, value)
		}
	}
}

func TestConcurrentUDIDCallbackOnlyConsumesChallengeOnce(t *testing.T) {
	env := newTestEnvironment(t, true)
	response, created := requestJSON(t, http.MethodPost, env.server.URL+"/api/udid/sessions", nil, nil)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d %+v", response.StatusCode, created)
	}
	challenge := created["challenge"].(string)
	callback := fmt.Sprintf(`<plist><dict><key>UDID</key><string>00008110-0012345678901234</string><key>CHALLENGE</key><string>%s</string></dict></plist>`, challenge)
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request, _ := http.NewRequest(http.MethodPut, env.server.URL+"/api/udid/callback", strings.NewReader(callback))
			result, err := http.DefaultClient.Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			defer result.Body.Close()
			statuses <- result.StatusCode
		}()
	}
	close(start)
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("callback statuses = %+v", counts)
	}
}

func TestUploadRejectsHashMismatchAndInvalidMagic(t *testing.T) {
	env := newTestEnvironment(t, false)
	content := []byte("PK\x03\x04content")
	metadata := packageMetadata(model.PlatformAndroid, "com.example.bad", "1", "1", content)
	metadata.SHA256 = strings.Repeat("0", 64)
	path := filepath.Join(t.TempDir(), "bad.apk")
	_ = os.WriteFile(path, content, 0o600)
	if _, err := env.apiClient.Upload(context.Background(), path, metadata, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("hash mismatch accepted: %v", err)
	}
	invalid := []byte("not-a-zip-package")
	metadata = packageMetadata(model.PlatformAndroid, "com.example.bad", "2", "2", invalid)
	_ = os.WriteFile(path, invalid, 0o600)
	if _, err := env.apiClient.Upload(context.Background(), path, metadata, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "ZIP-based") {
		t.Fatalf("invalid magic accepted: %v", err)
	}
	if len(env.repository.Apps()) != 0 {
		t.Fatal("invalid upload created an app")
	}
}

func TestTwentyConcurrentDownloads(t *testing.T) {
	env := newTestEnvironment(t, false)
	content := append([]byte("PK\x03\x04"), bytes.Repeat([]byte("x"), 256<<10)...)
	uploaded := upload(t, env, packageMetadata(model.PlatformAndroid, "com.example.concurrent", "1", "1", content), content)
	_, access := requestJSON(t, http.MethodPost, env.server.URL+"/api/public/apps/"+uploaded.App.Slug+"/access", map[string]string{}, nil)
	target := localize(env, access["downloadUrl"].(string))
	var wait sync.WaitGroup
	errorsChannel := make(chan error, 20)
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := http.Get(target)
			if err != nil {
				errorsChannel <- err
				return
			}
			data, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				errorsChannel <- readErr
				return
			}
			if response.StatusCode != http.StatusOK || !bytes.Equal(data, content) {
				errorsChannel <- fmt.Errorf("status %d or content mismatch", response.StatusCode)
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

func TestHTTPSRequirementAndSecurityHeaders(t *testing.T) {
	env := newTestEnvironment(t, false)
	env.cfg.AllowHTTP = false
	api, err := New(env.cfg, env.repository, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://hub.example/healthz", nil)
	recorder := httptest.NewRecorder()
	api.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || recorder.Header().Get("Strict-Transport-Security") == "" || recorder.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security middleware failed: %d %+v", recorder.Code, recorder.Header())
	}
}
