package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"myapphub/cli/internal/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestClientMethodsAndAuthorization(t *testing.T) {
	paths := make([]string, 0)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer "+strings.Repeat("t", 32) {
			t.Fatal("authorization header missing")
		}
		paths = append(paths, request.Method+" "+request.URL.Path)
		switch {
		case request.URL.Path == "/api/v1/apps":
			return response(200, `{"apps":[{"slug":"demo"}]}`), nil
		case strings.HasSuffix(request.URL.Path, "/password"):
			return response(200, `{"app":{"slug":"demo"}}`), nil
		case strings.HasSuffix(request.URL.Path, "/promote"):
			return response(200, `{"app":{"slug":"demo"}}`), nil
		case strings.HasSuffix(request.URL.Path, "/restore"):
			return response(200, `{"app":{"slug":"demo"}}`), nil
		default:
			return response(200, `{"deleted":true,"recoverableUntil":"2026-08-01T00:00:00Z"}`), nil
		}
	})
	client := &Client{BaseURL: "https://hub.example", Token: strings.Repeat("t", 32), HTTP: &http.Client{Transport: transport}}
	ctx := context.Background()
	if _, err := client.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Promote(ctx, "demo", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Delete(ctx, "demo", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DeleteApp(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Restore(ctx, "demo", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetPassword(ctx, "demo", "password"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 6 {
		t.Fatalf("unexpected requests: %v", paths)
	}
}

func TestClientValidationAndAPIError(t *testing.T) {
	if _, err := New("http://public.example", strings.Repeat("t", 32), time.Second); err == nil {
		t.Fatal("public HTTP accepted")
	}
	if _, err := New("https://hub.example", "weak", time.Second); err == nil {
		t.Fatal("weak token accepted")
	}
	client := &Client{BaseURL: "https://hub.example", Token: strings.Repeat("t", 32), HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(409, `{"code":"version_conflict","message":"conflict"}`), nil
	})}}
	_, err := client.List(context.Background())
	apiError, ok := err.(*APIError)
	if !ok || apiError.Status != 409 || apiError.Code != "version_conflict" {
		t.Fatalf("unexpected error: %#v", err)
	}
}

func TestChunkedUploadAndProgress(t *testing.T) {

	content := []byte("PK\x03\x04streamed-package")
	path := filepath.Join(t.TempDir(), "app.apk")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := model.UploadMetadata{Platform: model.PlatformAndroid, Identifier: "com.example.demo", Name: "Demo", VersionName: "1", BuildNumber: "1", FileSize: int64(len(content)), SHA256: strings.Repeat("a", 64), Promote: true}
	paths := make([]string, 0)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.Method+" "+request.URL.Path)
		switch request.URL.Path {
		case "/api/v1/uploads":
			var received model.UploadMetadata
			if json.NewDecoder(request.Body).Decode(&received) != nil || received.Identifier != metadata.Identifier {
				t.Fatal("metadata was not sent correctly")
			}
			return response(201, `{"id":"upload-id","partSize":8388608}`), nil
		case "/api/v1/uploads/upload-id/parts/1":
			data, _ := io.ReadAll(request.Body)
			if string(data) != string(content) || request.Header.Get("X-Part-SHA256") == "" {
				t.Fatal("package part or checksum mismatch")
			}
			return response(200, `{"partNumber":1,"etag":"test-etag"}`), nil
		case "/api/v1/uploads/upload-id/complete":
			var body struct {
				Parts []struct {
					PartNumber int
					ETag       string
				}
			}
			if json.NewDecoder(request.Body).Decode(&body) != nil || len(body.Parts) != 1 || body.Parts[0].ETag != "test-etag" {
				t.Fatal("completion parts mismatch")
			}
			return response(201, `{"app":{"slug":"demo"},"version":{"id":"v1"}}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	client := &Client{BaseURL: "https://hub.example", Token: strings.Repeat("t", 32), HTTP: &http.Client{Transport: transport}}
	progress := &strings.Builder{}
	result, err := client.Upload(context.Background(), path, metadata, nil, progress)
	if err != nil {
		t.Fatal(err)
	}
	if result.App.Slug != "demo" || !strings.Contains(progress.String(), "100.0%") || len(paths) != 3 {
		t.Fatalf("unexpected result/progress: %+v %q", result, progress.String())
	}
}

func TestDuplicateUploadRefreshesIcon(t *testing.T) {
	content := []byte("PK\x03\x04stored-package")
	path := filepath.Join(t.TempDir(), "app.ipa")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	icon := []byte("\x89PNG\r\n\x1a\nicon-bytes")
	metadata := model.UploadMetadata{Platform: model.PlatformIOS, Identifier: "com.example.demo", Name: "Demo", VersionName: "1", BuildNumber: "1", FileSize: int64(len(content)), SHA256: strings.Repeat("a", 64), Promote: true, IconMIME: "image/png"}
	refreshed := false
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/v1/uploads":
			return response(200, `{"duplicate":true,"nameUpdated":true,"app":{"slug":"demo","name":"Corrected name"},"version":{"id":"v1"}}`), nil
		case "/api/v1/apps/demo/ios/icon":
			if request.Method != http.MethodPut {
				t.Fatalf("icon refresh used %s", request.Method)
			}
			data, _ := io.ReadAll(request.Body)
			if string(data) != string(icon) {
				t.Fatal("icon bytes were not sent")
			}
			refreshed = true
			return response(200, `{"icon":"icon-refreshed.png"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	client := &Client{BaseURL: "https://hub.example", Token: strings.Repeat("t", 32), HTTP: &http.Client{Transport: transport}}
	result, err := client.Upload(context.Background(), path, metadata, icon, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Duplicate || !result.IconUpdated || !result.NameUpdated || result.App.Name != "Corrected name" || !refreshed {
		t.Fatalf("stored metadata was not refreshed: %+v", result)
	}
}
