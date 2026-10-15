package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"myapphub/cli/internal/model"
	"myapphub/cli/internal/packageinfo"
)

type fakeAPI struct {
	apps         []model.PublicApp
	uploadResult model.UploadResult
	called       string
	password     string
	err          error
}

func (f *fakeAPI) List(context.Context) ([]model.PublicApp, error) {
	f.called = "list"
	return f.apps, f.err
}
func (f *fakeAPI) Upload(_ context.Context, path string, metadata model.UploadMetadata, _ []byte, _ io.Writer) (model.UploadResult, error) {
	f.called = "upload:" + path + ":" + metadata.ReleaseNotes
	return f.uploadResult, f.err
}
func (f *fakeAPI) Promote(_ context.Context, slug, version string) (model.PublicApp, error) {
	f.called = "promote:" + slug + ":" + version
	return f.apps[0], f.err
}
func (f *fakeAPI) Delete(_ context.Context, slug, version string) (time.Time, error) {
	f.called = "delete:" + slug + ":" + version
	return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), f.err
}
func (f *fakeAPI) DeleteApp(_ context.Context, slug string) (time.Time, error) {
	f.called = "delete-app:" + slug
	return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), f.err
}
func (f *fakeAPI) Restore(_ context.Context, slug, version string) (model.PublicApp, error) {
	f.called = "restore:" + slug + ":" + version
	return f.apps[0], f.err
}
func (f *fakeAPI) SetPassword(_ context.Context, slug, password string) (model.PublicApp, error) {
	f.called = "password:" + slug
	f.password = password
	return f.apps[0], f.err
}

type fakeParser struct {
	options packageinfo.Options
	result  packageinfo.Result
	err     error
}

func (f *fakeParser) Parse(_ context.Context, _ string, options packageinfo.Options) (packageinfo.Result, error) {
	f.options = options
	f.result.Metadata.ReleaseNotes = options.ReleaseNotes
	f.result.Metadata.Slug = options.Slug
	f.result.Metadata.Promote = options.Promote
	return f.result, f.err
}

func fixture() (*fakeAPI, *fakeParser, *bytes.Buffer, *bytes.Buffer) {
	version := model.Version{ID: "1.0-1-aabbccdd", VersionName: "1.0", BuildNumber: "1", SHA256: strings.Repeat("a", 64), FileSize: 123}
	app := model.PublicApp{Slug: "demo", Platform: model.PlatformAndroid, Identifier: "com.example.demo", Name: "Demo", CurrentVersionID: version.ID, Versions: []model.Version{version}}
	api := &fakeAPI{apps: []model.PublicApp{app}, uploadResult: model.UploadResult{App: app, Version: version}}
	parser := &fakeParser{result: packageinfo.Result{Metadata: model.UploadMetadata{Platform: model.PlatformAndroid, Identifier: app.Identifier, Name: app.Name, VersionName: "1.0", BuildNumber: "1"}}}
	return api, parser, &bytes.Buffer{}, &bytes.Buffer{}
}

func execute(t *testing.T, args ...string) (*fakeAPI, *fakeParser, string, string, error) {
	t.Helper()
	api, parser, out, stderr := fixture()
	root := NewRoot(Dependencies{API: api, Parser: parser, Out: out, Err: stderr, In: strings.NewReader("secret\n"), ReadPassword: func(io.Reader, io.Writer) (string, error) { return "hidden password", nil }})
	root.SetArgs(args)
	err := root.Execute()
	return api, parser, out.String(), stderr.String(), err
}

func TestCommandTreeAndVersion(t *testing.T) {
	_, _, output, _, err := execute(t, "version")
	if err != nil || strings.TrimSpace(output) != Version {
		t.Fatalf("version failed: %v %q", err, output)
	}
	_, _, completion, _, err := execute(t, "completion", "bash")
	if err != nil || !strings.Contains(completion, "bash completion") {
		t.Fatalf("completion failed: %v", err)
	}
}

func TestLegacyPublicURLDoesNotOverrideProduction(t *testing.T) {
	t.Setenv("APPHUB_SERVER", "")
	t.Setenv("APPHUB_PUBLIC_URL", "http://127.0.0.1:8787")
	root := NewRoot(Dependencies{Out: io.Discard, Err: io.Discard})
	value, err := root.PersistentFlags().GetString("server")
	if err != nil || value != defaultServerURL {
		t.Fatalf("server default = %q, %v", value, err)
	}
}

func TestServerDefaultsToProduction(t *testing.T) {
	t.Setenv("APPHUB_SERVER", "")
	t.Setenv("APPHUB_PUBLIC_URL", "")
	root := NewRoot(Dependencies{Out: io.Discard, Err: io.Discard})
	value, err := root.PersistentFlags().GetString("server")
	if err != nil || value != defaultServerURL {
		t.Fatalf("production server default = %q, %v", value, err)
	}
}

func TestServerEnvironmentOverride(t *testing.T) {
	t.Setenv("APPHUB_SERVER", "http://127.0.0.1:8787")
	root := NewRoot(Dependencies{Out: io.Discard, Err: io.Discard})
	value, err := root.PersistentFlags().GetString("server")
	if err != nil || value != "http://127.0.0.1:8787" {
		t.Fatalf("server override = %q, %v", value, err)
	}
}

func TestHelpDoesNotExposeEnvironmentToken(t *testing.T) {
	secret := strings.Repeat("s", 40)
	t.Setenv("APPHUB_TOKEN", secret)
	var output bytes.Buffer
	root := NewRoot(Dependencies{Out: &output, Err: io.Discard})
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil || strings.Contains(output.String(), secret) {
		t.Fatalf("help exposed management token: %v", err)
	}
}

func TestListInfoAndJSONOutput(t *testing.T) {
	_, _, output, _, err := execute(t, "list", "demo")
	if err != nil || !strings.Contains(output, "com.example.demo") || !strings.Contains(output, "1.0-1-aabbccdd") {
		t.Fatalf("list failed: %v %q", err, output)
	}
	_, _, output, _, err = execute(t, "info", "demo", "1.0-1-aabbccdd")
	if err != nil || !strings.Contains(output, "SHA-256") {
		t.Fatalf("info failed: %v %q", err, output)
	}
	_, _, output, stderr, err := execute(t, "--json", "list")
	if err != nil || stderr != "" {
		t.Fatalf("JSON mode polluted stderr: %v %q", err, stderr)
	}
	var value map[string]any
	if json.Unmarshal([]byte(output), &value) != nil || value["apps"] == nil {
		t.Fatalf("invalid JSON: %q", output)
	}
	_, _, _, _, err = execute(t, "list", "missing")
	if err == nil {
		t.Fatal("missing app did not fail")
	}
}

func TestUploadFlagsAndErrors(t *testing.T) {
	api, parser, output, stderr, err := execute(t, "upload", "demo.apk", "--notes", "release", "--slug", "custom", "--no-promote")
	if err != nil {
		t.Fatal(err)
	}
	if api.called != "upload:demo.apk:release" || parser.options.Slug != "custom" || parser.options.Promote || !strings.Contains(output, "demo 1.0 (1)") || !strings.Contains(stderr, "正在验证") {
		t.Fatalf("unexpected upload: %s %+v %q %q", api.called, parser.options, output, stderr)
	}
	_, _, _, _, err = execute(t, "upload", "demo.apk", "--notes", "a", "--notes-file", "b")
	if err == nil {
		t.Fatal("mutually exclusive notes accepted")
	}
}

func TestLifecycleCommands(t *testing.T) {
	api, _, _, _, err := execute(t, "promote", "demo", "v1")
	if err != nil || api.called != "promote:demo:v1" {
		t.Fatalf("promote failed: %v %s", err, api.called)
	}
	api, _, _, _, err = execute(t, "delete", "demo", "v1")
	if err == nil || api.called != "" {
		t.Fatal("delete without confirmation executed")
	}
	api, _, output, _, err := execute(t, "delete", "demo", "v1", "--yes")
	if err != nil || api.called != "delete:demo:v1" || !strings.Contains(output, "2026-08-01") {
		t.Fatalf("delete failed: %v %s %q", err, api.called, output)
	}
	api, _, output, _, err = execute(t, "delete", "demo", "--yes")
	if err != nil || api.called != "delete-app:demo" || !strings.Contains(output, "整个应用") {
		t.Fatalf("delete app failed: %v %s %q", err, api.called, output)
	}
	api, _, _, _, err = execute(t, "restore", "demo", "v1")
	if err != nil || api.called != "restore:demo:v1" {
		t.Fatalf("restore failed: %v", err)
	}
}

func TestPasswordInputAndClear(t *testing.T) {
	api, _, _, _, err := execute(t, "password", "demo")
	if err != nil || api.password != "hidden password" {
		t.Fatalf("password failed: %v %q", err, api.password)
	}
	api, _, _, _, err = execute(t, "password", "demo", "--clear")
	if err != nil || api.password != "" {
		t.Fatalf("password clear failed: %v %q", err, api.password)
	}
}

func TestAPIErrorIsReturnedWithoutUsageNoise(t *testing.T) {
	api, parser, out, stderr := fixture()
	api.err = errors.New("server unavailable")
	root := NewRoot(Dependencies{API: api, Parser: parser, Out: out, Err: stderr})
	root.SetArgs([]string{"list"})
	if err := root.Execute(); err == nil || err.Error() != "server unavailable" {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stderr.String(), "Usage:") {
		t.Fatal("usage printed for business error")
	}
}
