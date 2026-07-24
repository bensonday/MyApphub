package packageinfo

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	run func(string, ...string) ([]byte, error)
}

func (f fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return f.run(name, args...)
}

func makeZIP(t *testing.T, extension string, files map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package"+extension)
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(output)
	for name, data := range files {
		part, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func png() []byte { return append([]byte("\x89PNG\r\n\x1a\n"), []byte("image")...) }

func TestExecRunnerKeepsSuccessfulStderrOutOfParsedOutput(t *testing.T) {
	output, err := (ExecRunner{}).Run(
		context.Background(),
		"/bin/sh",
		"-c",
		`printf 'com.example.demo\n'; printf 'tool warning\n' >&2`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != "com.example.demo" {
		t.Fatalf("stderr contaminated parsed output: %q", got)
	}
}

func TestExecRunnerReportsStderrOnFailure(t *testing.T) {
	_, err := (ExecRunner{}).Run(
		context.Background(),
		"/bin/sh",
		"-c",
		`printf 'tool failure\n' >&2; exit 2`,
	)
	if err == nil || !strings.Contains(err.Error(), "tool failure") {
		t.Fatalf("stderr diagnostic was not preserved: %v", err)
	}
}

func TestParseAPK(t *testing.T) {
	sdk := t.TempDir()
	analyzer := filepath.Join(sdk, "cmdline-tools", "latest", "bin", "apkanalyzer")
	signer := filepath.Join(sdk, "build-tools", "36.0.0", "apksigner")
	for _, path := range []string{analyzer, signer} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("tool"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		if strings.HasSuffix(name, "apksigner") {
			return []byte("Verifies\nSigner #1 certificate SHA-256 digest: AABBCCDD\n"), nil
		}
		key := strings.Join(args[:len(args)-1], " ")
		values := map[string]string{"manifest application-id": "com.example.demo", "manifest version-name": "1.2.3", "manifest version-code": "12", "manifest min-sdk": "24"}
		if value, ok := values[key]; ok {
			return []byte(value), nil
		}
		return nil, fmt.Errorf("unexpected command: %s", key)
	}}
	path := makeZIP(t, ".apk", map[string][]byte{"AndroidManifest.xml": {1, 2, 3}, "res/mipmap-xxxhdpi/ic_launcher.png": png()})
	parser := &Parser{Runner: runner, AndroidSDK: sdk}
	result, err := parser.Parse(context.Background(), path, Options{Promote: true, ReleaseNotes: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Identifier != "com.example.demo" || result.Metadata.VersionName != "1.2.3" || result.Metadata.SigningCertificateSHA256 != "aabbccdd" || result.Metadata.SHA256 == "" || !result.Metadata.Promote {
		t.Fatalf("unexpected metadata: %+v", result.Metadata)
	}
	if string(result.Icon[:8]) != "\x89PNG\r\n\x1a\n" || result.Metadata.IconMIME != "image/png" {
		t.Fatal("APK icon was not extracted")
	}
}

func TestParseEnterpriseAndAdHocIPA(t *testing.T) {
	for _, test := range []struct{ name, allDevices, devices, expected string }{{"enterprise", "true", "", "enterprise"}, {"ad-hoc", "false", `["UDID"]`, "ad-hoc"}} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
				switch filepath.Base(name) {
				case "codesign":
					return nil, nil
				case "security":
					return []byte("<?xml version=\"1.0\"?><plist><dict/></plist>"), nil
				case "plutil":
					key := args[1]
					values := map[string]string{"CFBundleIdentifier": "com.example.ios", "CFBundleShortVersionString": "2.0", "CFBundleVersion": "20", "CFBundleDisplayName": "iOS Demo", "MinimumOSVersion": "15.0", "ProvisionsAllDevices": test.allDevices, "ProvisionedDevices": test.devices, "Entitlements.get-task-allow": "false", "ExpirationDate": "2027-01-02T03:04:05Z"}
					if value := values[key]; value != "" {
						return []byte(value), nil
					}
					return nil, fmt.Errorf("missing key")
				}
				return nil, fmt.Errorf("unexpected tool")
			}}
			path := makeZIP(t, ".ipa", map[string][]byte{"Payload/Demo.app/Info.plist": []byte("plist"), "Payload/Demo.app/embedded.mobileprovision": []byte("profile"), "Payload/Demo.app/AppIcon60x60@3x.png": png()})
			parser := &Parser{Runner: runner}
			result, err := parser.Parse(context.Background(), path, Options{Promote: true})
			if err != nil {
				t.Fatal(err)
			}
			if result.Metadata.DistributionType != test.expected || result.Metadata.Identifier != "com.example.ios" || result.Metadata.ProvisioningExpiresAt != "2027-01-02T03:04:05Z" {
				t.Fatalf("unexpected metadata: %+v", result.Metadata)
			}
		})
	}
}

func TestRejectsUnsupportedIPAAndUnsafeZIP(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		if filepath.Base(name) == "codesign" {
			return nil, nil
		}
		if filepath.Base(name) == "security" {
			return []byte("plist"), nil
		}
		if filepath.Base(name) == "plutil" {
			values := map[string]string{"CFBundleIdentifier": "com.example.ios", "CFBundleShortVersionString": "1", "CFBundleVersion": "1", "CFBundleDisplayName": "Demo"}
			if value := values[args[1]]; value != "" {
				return []byte(value), nil
			}
			return nil, fmt.Errorf("missing")
		}
		return nil, fmt.Errorf("unexpected")
	}}
	path := makeZIP(t, ".ipa", map[string][]byte{"Payload/Demo.app/Info.plist": []byte("plist"), "Payload/Demo.app/embedded.mobileprovision": []byte("profile")})
	if _, err := (&Parser{Runner: runner}).Parse(context.Background(), path, Options{Promote: true}); err == nil || !strings.Contains(err.Error(), "app-store") {
		t.Fatalf("unsupported IPA accepted: %v", err)
	}
	unsafe := makeZIP(t, ".apk", map[string][]byte{"../escape": []byte("bad")})
	if _, err := (&Parser{Runner: runner}).Parse(context.Background(), unsafe, Options{}); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe ZIP accepted: %v", err)
	}
}

func TestIconOverrideValidationAndMissingAndroidTools(t *testing.T) {
	path := makeZIP(t, ".apk", map[string][]byte{"AndroidManifest.xml": []byte("x")})
	icon := filepath.Join(t.TempDir(), "icon.png")
	_ = os.WriteFile(icon, []byte("not-png"), 0o600)
	parser := &Parser{Runner: fakeRunner{run: func(string, ...string) ([]byte, error) { return nil, nil }}, AndroidSDK: t.TempDir()}
	if _, err := parser.Parse(context.Background(), path, Options{IconPath: icon}); err == nil || !strings.Contains(err.Error(), "apkanalyzer") {
		t.Fatalf("missing tool not reported: %v", err)
	}
	if _, _, err := readIcon(icon); err == nil {
		t.Fatal("invalid icon accepted")
	}
}
