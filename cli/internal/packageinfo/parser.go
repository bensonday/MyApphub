package packageinfo

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"myapphub/cli/internal/model"
)

const maxExpandedSize = uint64(4 << 30)

type Options struct {
	Name, IconPath, Slug, ReleaseNotes string
	Promote                            bool
}
type Result struct {
	Metadata model.UploadMetadata
	Icon     []byte
}

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.Output()
	if err != nil {
		detail := ""
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			detail = strings.TrimSpace(string(exitError.Stderr))
		}
		if detail == "" {
			detail = strings.TrimSpace(string(output))
		}
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("%s failed: %s", filepath.Base(name), detail)
	}
	return output, nil
}

type Parser struct {
	Runner     Runner
	HomeDir    string
	AndroidSDK string
}

func New() *Parser {
	home, _ := os.UserHomeDir()
	return &Parser{Runner: ExecRunner{}, HomeDir: home, AndroidSDK: first(os.Getenv("ANDROID_SDK_ROOT"), os.Getenv("ANDROID_HOME"))}
}

func (p *Parser) Parse(ctx context.Context, packagePath string, options Options) (Result, error) {
	extension := strings.ToLower(filepath.Ext(packagePath))
	if extension != ".ipa" && extension != ".apk" {
		return Result{}, errors.New("only .ipa and .apk files are supported")
	}
	info, err := os.Stat(packagePath)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 4 {
		return Result{}, errors.New("package is empty or invalid")
	}
	archive, entries, err := inspectZip(packagePath)
	if err != nil {
		return Result{}, err
	}
	defer archive.Close()
	var result Result
	if extension == ".ipa" {
		result, err = p.parseIPA(ctx, packagePath, entries, options)
	} else {
		result, err = p.parseAPK(ctx, packagePath, entries, options)
	}
	if err != nil {
		return Result{}, err
	}
	file, err := os.Open(packagePath)
	if err != nil {
		return Result{}, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	closeErr := file.Close()
	if err != nil {
		return Result{}, err
	}
	if closeErr != nil {
		return Result{}, closeErr
	}
	result.Metadata.SHA256 = hex.EncodeToString(hash.Sum(nil))
	result.Metadata.FileSize = info.Size()
	result.Metadata.Slug = options.Slug
	result.Metadata.ReleaseNotes = options.ReleaseNotes
	result.Metadata.Promote = options.Promote
	if options.IconPath != "" {
		result.Icon, result.Metadata.IconMIME, err = readIcon(options.IconPath)
		if err != nil {
			return Result{}, err
		}
	}
	return result, nil
}

func inspectZip(path string) (*zip.ReadCloser, []*zip.File, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, errors.New("package is not a valid ZIP archive")
	}
	if len(archive.File) == 0 || len(archive.File) > 100_000 {
		archive.Close()
		return nil, nil, errors.New("package contains an invalid number of entries")
	}
	var expanded uint64
	for _, file := range archive.File {
		clean := filepath.ToSlash(filepath.Clean(file.Name))
		if strings.HasPrefix(file.Name, "/") || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(file.Name, 0) {
			archive.Close()
			return nil, nil, errors.New("package contains an unsafe path")
		}
		expanded += file.UncompressedSize64
		if expanded > maxExpandedSize {
			archive.Close()
			return nil, nil, errors.New("expanded package exceeds 4 GB")
		}
		if file.Mode()&os.ModeSymlink != 0 {
			archive.Close()
			return nil, nil, errors.New("package contains a symbolic link")
		}
	}
	return archive, archive.File, nil
}

func (p *Parser) parseIPA(ctx context.Context, packagePath string, entries []*zip.File, options Options) (Result, error) {
	temp, err := os.MkdirTemp("", "apphub-ipa-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(temp)
	if err := extractZip(entries, temp); err != nil {
		return Result{}, err
	}
	payload := filepath.Join(temp, "Payload")
	children, err := os.ReadDir(payload)
	if err != nil {
		return Result{}, errors.New("IPA is missing Payload directory")
	}
	apps := make([]string, 0)
	for _, child := range children {
		if child.IsDir() && strings.HasSuffix(child.Name(), ".app") {
			apps = append(apps, filepath.Join(payload, child.Name()))
		}
	}
	if len(apps) != 1 {
		return Result{}, errors.New("IPA must contain exactly one .app")
	}
	appDir := apps[0]
	if _, err := p.Runner.Run(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", appDir); err != nil {
		return Result{}, fmt.Errorf("IPA signature verification failed: %w", err)
	}
	infoPath := filepath.Join(appDir, "Info.plist")
	identifier := p.plutil(ctx, infoPath, "CFBundleIdentifier")
	version := p.plutil(ctx, infoPath, "CFBundleShortVersionString")
	build := p.plutil(ctx, infoPath, "CFBundleVersion")
	name := options.Name
	if name == "" {
		name = p.plutil(ctx, infoPath, "CFBundleDisplayName")
	}
	if name == "" {
		name = p.plutil(ctx, infoPath, "CFBundleName")
	}
	if identifier == "" || version == "" || build == "" || name == "" {
		return Result{}, errors.New("IPA Info.plist is missing required fields")
	}
	profileBytes, err := p.Runner.Run(ctx, "/usr/bin/security", "cms", "-D", "-i", filepath.Join(appDir, "embedded.mobileprovision"))
	if err != nil {
		return Result{}, errors.New("IPA is missing a readable embedded.mobileprovision")
	}
	profilePath := filepath.Join(temp, "profile.plist")
	if err := os.WriteFile(profilePath, profileBytes, 0o600); err != nil {
		return Result{}, err
	}
	distribution := ""
	allDevices := p.plutil(ctx, profilePath, "ProvisionsAllDevices")
	devices := p.plutilJSON(ctx, profilePath, "ProvisionedDevices")
	development := p.plutil(ctx, profilePath, "Entitlements.get-task-allow")
	if allDevices == "true" {
		distribution = "enterprise"
	} else if devices != "" && development != "true" {
		distribution = "ad-hoc"
	} else if development == "true" {
		distribution = "development"
	} else {
		distribution = "app-store"
	}
	if distribution != "enterprise" && distribution != "ad-hoc" {
		return Result{}, fmt.Errorf("unsupported IPA distribution type: %s", distribution)
	}
	result := Result{Metadata: model.UploadMetadata{Platform: model.PlatformIOS, Identifier: identifier, Name: name, VersionName: version, BuildNumber: build, MinimumOSVersion: p.plutil(ctx, infoPath, "MinimumOSVersion"), DistributionType: distribution}}
	expires := p.plutil(ctx, profilePath, "ExpirationDate")
	if parsed := normalizeDate(expires); parsed != "" {
		result.Metadata.ProvisioningExpiresAt = parsed
	}
	if options.IconPath == "" {
		result.Icon, result.Metadata.IconMIME = findIPAIcon(entries)
	}
	return result, nil
}

func (p *Parser) parseAPK(ctx context.Context, packagePath string, entries []*zip.File, options Options) (Result, error) {
	analyzer, signer, err := p.androidTools()
	if err != nil {
		return Result{}, err
	}
	signature, err := p.Runner.Run(ctx, signer, "verify", "--verbose", "--print-certs", packagePath)
	if err != nil || !strings.Contains(string(signature), "Verifies") {
		return Result{}, errors.New("APK signature verification failed")
	}
	command := func(args ...string) string {
		output, runErr := p.Runner.Run(ctx, analyzer, append(args, packagePath)...)
		if runErr != nil {
			return ""
		}
		return strings.TrimSpace(string(output))
	}
	identifier := command("manifest", "application-id")
	version := command("manifest", "version-name")
	build := command("manifest", "version-code")
	minimum := command("manifest", "min-sdk")
	if identifier == "" || version == "" || build == "" {
		return Result{}, errors.New("APK manifest is missing required fields")
	}
	name := options.Name
	if name == "" {
		name = identifier[strings.LastIndex(identifier, ".")+1:]
	}
	certificate := ""
	match := regexp.MustCompile(`(?i)certificate SHA-256 digest:\s*([a-f0-9]+)`).FindStringSubmatch(string(signature))
	if len(match) == 2 {
		certificate = strings.ToLower(match[1])
	}
	result := Result{Metadata: model.UploadMetadata{Platform: model.PlatformAndroid, Identifier: identifier, Name: name, VersionName: version, BuildNumber: build, MinimumOSVersion: minimum, SigningCertificateSHA256: certificate}}
	if options.IconPath == "" {
		result.Icon, result.Metadata.IconMIME = findAPKIcon(entries)
	}
	return result, nil
}

func (p *Parser) plutil(ctx context.Context, path, key string) string {
	output, err := p.Runner.Run(ctx, "/usr/bin/plutil", "-extract", key, "raw", "-o", "-", path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
func (p *Parser) plutilJSON(ctx context.Context, path, key string) string {
	output, err := p.Runner.Run(ctx, "/usr/bin/plutil", "-extract", key, "json", "-o", "-", path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
func (p *Parser) androidTools() (string, string, error) {
	sdk := p.AndroidSDK
	if sdk == "" {
		sdk = filepath.Join(p.HomeDir, "Library", "Android", "sdk")
	}
	analyzer := filepath.Join(sdk, "cmdline-tools", "latest", "bin", "apkanalyzer")
	builds, _ := filepath.Glob(filepath.Join(sdk, "build-tools", "*", "apksigner"))
	sort.Sort(sort.Reverse(sort.StringSlice(builds)))
	if _, err := os.Stat(analyzer); err != nil {
		return "", "", fmt.Errorf("apkanalyzer not found under %s", sdk)
	}
	if len(builds) == 0 {
		return "", "", fmt.Errorf("apksigner not found under %s", sdk)
	}
	return analyzer, builds[0], nil
}

func extractZip(entries []*zip.File, target string) error {
	for _, file := range entries {
		path := filepath.Join(target, filepath.FromSlash(file.Name))
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeIn, closeOut := input.Close(), output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeIn != nil {
			return closeIn
		}
		if closeOut != nil {
			return closeOut
		}
	}
	return nil
}
func findIPAIcon(entries []*zip.File) ([]byte, string) {
	candidates := filterIcons(entries, func(name string) bool {
		return strings.Contains(name, ".app/") && strings.HasSuffix(strings.ToLower(name), ".png") && (strings.Contains(strings.ToLower(name), "appicon") || strings.Contains(strings.ToLower(name), "icon"))
	})
	return readZipIcon(candidates)
}
func findAPKIcon(entries []*zip.File) ([]byte, string) {
	candidates := filterIcons(entries, func(name string) bool {
		lower := strings.ToLower(name)
		return strings.HasPrefix(lower, "res/mipmap") && strings.HasSuffix(lower, ".png") && (strings.Contains(lower, "launcher") || strings.Contains(lower, "icon"))
	})
	return readZipIcon(candidates)
}
func filterIcons(entries []*zip.File, predicate func(string) bool) []*zip.File {
	result := make([]*zip.File, 0)
	for _, file := range entries {
		if predicate(file.Name) && file.UncompressedSize64 <= 2<<20 {
			result = append(result, file)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name > result[j].Name })
	return result
}
func readZipIcon(candidates []*zip.File) ([]byte, string) {
	if len(candidates) == 0 {
		return nil, ""
	}
	input, err := candidates[0].Open()
	if err != nil {
		return nil, ""
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, 2<<20))
	if err != nil || !validPNG(data) {
		return nil, ""
	}
	return data, "image/png"
}
func readIcon(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 2<<20 {
		return nil, "", errors.New("icon exceeds 2 MB")
	}
	if validPNG(data) {
		return data, "image/png", nil
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return data, "image/jpeg", nil
	}
	return nil, "", errors.New("icon must be PNG or JPEG")
}
func validPNG(data []byte) bool { return len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" }
func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
func normalizeDate(value string) string {
	for _, layout := range []string{"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05 -0700"} {
		if parsed, err := timeParse(layout, value); err == nil {
			return parsed
		}
	}
	return ""
}

var timeParse = func(layout, value string) (string, error) {
	parsed, err := time.Parse(layout, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339), nil
}
