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

func pngWithSize(width, height int) []byte {
	data := append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 13)
	data = append(data, []byte("IHDR")...)
	data = append(data, byte(width>>24), byte(width>>16), byte(width>>8), byte(width))
	data = append(data, byte(height>>24), byte(height>>16), byte(height>>8), byte(height))
	return append(data, []byte("filler")...)
}

func webpWithSize(width, height int) []byte {
	data := append([]byte("RIFF"), 0, 0, 0, 0)
	data = append(data, []byte("WEBP")...)
	data = append(data, []byte("VP8X")...)
	data = append(data, 10, 0, 0, 0)
	data = append(data, 0, 0, 0, 0)
	data = append(data, byte(width-1), byte((width-1)>>8), byte((width-1)>>16))
	data = append(data, byte(height-1), byte((height-1)>>8), byte((height-1)>>16))
	return append(data, 0, 0, 0, 0)
}

func appleOptimizedPNG(width, height int) []byte {
	data := append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 4)
	data = append(data, []byte("CgBI")...)
	data = append(data, 0x50, 0x00, 0x20, 0x06) // chunk data
	data = append(data, 0, 0, 0, 0)             // chunk CRC
	return append(data, pngWithSize(width, height)[8:]...)
}

func TestImageDimensionsHandlesAppleOptimizedPNG(t *testing.T) {
	if width, height := imageDimensions(appleOptimizedPNG(120, 120), "image/png"); width != 120 || height != 120 {
		t.Fatalf("expected 120x120, got %dx%d", width, height)
	}
}

func TestNormalizeIconConvertsAppleOptimizedPNG(t *testing.T) {
	converted := pngWithSize(120, 120)
	parser := &Parser{Runner: fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		if filepath.Base(name) != "sips" {
			return nil, fmt.Errorf("unexpected tool: %s", name)
		}
		if err := os.WriteFile(args[len(args)-1], converted, 0o600); err != nil {
			return nil, err
		}
		return nil, nil
	}}}
	icon, mime := parser.normalizeIcon(context.Background(), appleOptimizedPNG(120, 120), "image/png")
	if mime != "image/png" || string(icon) != string(converted) {
		t.Fatal("Apple-optimized icon was not converted to a standard PNG")
	}
	plain := pngWithSize(120, 120)
	if icon, _ := parser.normalizeIcon(context.Background(), plain, "image/png"); string(icon) != string(plain) {
		t.Fatal("standard PNG should be stored unchanged")
	}
}

func zipEntries(t *testing.T, path string) []*zip.File {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { archive.Close() })
	return archive.File
}

func TestFindIPAIconIgnoresNestedBundlesAndPrefersPrimaryIcon(t *testing.T) {
	rootPhone := pngWithSize(120, 120)
	path := makeZIP(t, ".zip", map[string][]byte{
		"Payload/Demo.app/AppIcon60x60@2x.png":      rootPhone,
		"Payload/Demo.app/AppIcon76x76@2x~ipad.png": pngWithSize(152, 152),
		"Payload/Demo.app/Frameworks/App.framework/flutter_assets/packages/feature/assets/unit/speak_record_icon.png": pngWithSize(80, 80),
		"Payload/Demo.app/PlugIns/Widget.app/AppIcon40x40@2x.png":                                                     pngWithSize(240, 240),
	})
	icon, mime := findIPAIcon(zipEntries(t, path), []string{"AppIcon60x60", "AppIcon"})
	if mime != "image/png" || string(icon) != string(rootPhone) {
		t.Fatalf("expected the bundle root AppIcon60x60@2x.png, got %d bytes of %s", len(icon), mime)
	}
}

func TestFindIPAIconFallsBackToLargestPhoneIcon(t *testing.T) {
	largest := pngWithSize(167, 167)
	path := makeZIP(t, ".zip", map[string][]byte{
		"Payload/Demo.app/Icon-App-40x40@3x.png":      pngWithSize(120, 120),
		"Payload/Demo.app/Icon-App-83.5x83.5@2x.png":  largest,
		"Payload/Demo.app/Icon-App-76x76@2x~ipad.png": pngWithSize(152, 152),
	})
	icon, _ := findIPAIcon(zipEntries(t, path), nil)
	if string(icon) != string(largest) {
		t.Fatal("expected the largest non-iPad app bundle icon")
	}
}

func TestFindAPKIconSkipsAdaptiveLayersAndPrefersLargestLauncher(t *testing.T) {
	launcher := pngWithSize(192, 192)
	path := makeZIP(t, ".zip", map[string][]byte{
		"res/mipmap-mdpi/ic_launcher.png":               pngWithSize(48, 48),
		"res/mipmap-xxxhdpi/ic_launcher.png":            launcher,
		"res/mipmap-xxxhdpi/ic_launcher_round.png":      pngWithSize(192, 192),
		"res/mipmap-xxxhdpi/ic_launcher_foreground.png": pngWithSize(432, 432),
		"res/mipmap-xxxhdpi/ic_launcher_background.png": pngWithSize(432, 432),
	})
	icon, mime := findAPKIcon(zipEntries(t, path))
	if mime != "image/png" || string(icon) != string(launcher) {
		t.Fatal("expected the largest plain launcher icon")
	}
}

func TestFindAPKIconAcceptsWebPLauncher(t *testing.T) {
	icon, mime := findAPKIcon(zipEntries(t, makeZIP(t, ".zip", map[string][]byte{
		"res/mipmap-xxhdpi/ic_launcher.webp": webpWithSize(144, 144),
	})))
	if mime != "image/webp" || len(icon) == 0 {
		t.Fatalf("expected a WebP launcher icon, got %d bytes of %s", len(icon), mime)
	}
}

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

func TestParseSupportedIPA(t *testing.T) {
	for _, test := range []struct{ name, allDevices, devices, development, expected string }{
		{"enterprise", "true", "", "false", "enterprise"},
		{"ad-hoc", "false", `["UDID"]`, "false", "ad-hoc"},
		{"development", "false", `["UDID"]`, "true", "development"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
				switch filepath.Base(name) {
				case "codesign":
					return nil, nil
				case "security":
					return []byte("<?xml version=\"1.0\"?><plist><dict/></plist>"), nil
				case "plutil":
					key := args[1]
					values := map[string]string{"CFBundleIdentifier": "com.example.ios", "CFBundleShortVersionString": "2.0", "CFBundleVersion": "20", "CFBundleDisplayName": "iOS Demo", "MinimumOSVersion": "15.0", "ProvisionsAllDevices": test.allDevices, "ProvisionedDevices": test.devices, "Entitlements.get-task-allow": test.development, "ExpirationDate": "2027-01-02T03:04:05Z"}
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
