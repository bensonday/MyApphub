package packageinfo

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
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
	} else if devices != "" && development == "true" {
		distribution = "development"
	} else if devices != "" {
		distribution = "ad-hoc"
	} else {
		distribution = "app-store"
	}
	if distribution != "enterprise" && distribution != "ad-hoc" && distribution != "development" {
		return Result{}, fmt.Errorf("unsupported IPA distribution type: %s", distribution)
	}
	result := Result{Metadata: model.UploadMetadata{Platform: model.PlatformIOS, Identifier: identifier, Name: name, VersionName: version, BuildNumber: build, MinimumOSVersion: p.plutil(ctx, infoPath, "MinimumOSVersion"), DistributionType: distribution}}
	expires := p.plutil(ctx, profilePath, "ExpirationDate")
	if parsed := normalizeDate(expires); parsed != "" {
		result.Metadata.ProvisioningExpiresAt = parsed
	}
	if options.IconPath == "" {
		result.Icon, result.Metadata.IconMIME = findIPAIcon(entries, p.primaryIconNames(ctx, infoPath))
	}
	result.Icon, result.Metadata.IconMIME = p.normalizeIcon(ctx, result.Icon, result.Metadata.IconMIME)
	return result, nil
}

func (p *Parser) parseAPK(ctx context.Context, packagePath string, entries []*zip.File, options Options) (Result, error) {
	analyzer, signer, aapt, err := p.androidTools()
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
	badgingOutput, badgingErr := p.Runner.Run(ctx, aapt, "dump", "badging", packagePath)
	badging := parseAPKBadging(badgingOutput)
	name := options.Name
	if name == "" {
		name = badging.label
	}
	if name == "" {
		if badgingErr != nil {
			return Result{}, fmt.Errorf("APK application label could not be read: %w; use --name to override", badgingErr)
		}
		return Result{}, errors.New("APK has no readable application label; use --name to override")
	}
	certificate := ""
	match := regexp.MustCompile(`(?i)certificate SHA-256 digest:\s*([a-f0-9]+)`).FindStringSubmatch(string(signature))
	if len(match) == 2 {
		certificate = strings.ToLower(match[1])
	}
	result := Result{Metadata: model.UploadMetadata{Platform: model.PlatformAndroid, Identifier: identifier, Name: name, VersionName: version, BuildNumber: build, MinimumOSVersion: minimum, SigningCertificateSHA256: certificate}}
	if options.IconPath == "" {
		result.Icon, result.Metadata.IconMIME = findAPKIcon(entries, badging.icons)
		if len(result.Icon) == 0 {
			if badgingErr != nil {
				return Result{}, fmt.Errorf("APK application icon could not be read: %w; use --icon to override", badgingErr)
			}
			return Result{}, errors.New("APK has no readable application icon; use --icon with a PNG, JPEG or WebP file")
		}
	}
	result.Icon, result.Metadata.IconMIME = p.normalizeIcon(ctx, result.Icon, result.Metadata.IconMIME)
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
func (p *Parser) androidTools() (string, string, string, error) {
	sdk := p.AndroidSDK
	if sdk == "" {
		sdk = filepath.Join(p.HomeDir, "Library", "Android", "sdk")
	}
	analyzer := filepath.Join(sdk, "cmdline-tools", "latest", "bin", "apkanalyzer")
	builds, _ := filepath.Glob(filepath.Join(sdk, "build-tools", "*", "apksigner"))
	aapts, _ := filepath.Glob(filepath.Join(sdk, "build-tools", "*", "aapt"))
	sort.Sort(sort.Reverse(sort.StringSlice(builds)))
	sort.Sort(sort.Reverse(sort.StringSlice(aapts)))
	if _, err := os.Stat(analyzer); err != nil {
		return "", "", "", fmt.Errorf("apkanalyzer not found under %s", sdk)
	}
	if len(builds) == 0 {
		return "", "", "", fmt.Errorf("apksigner not found under %s", sdk)
	}
	if len(aapts) == 0 {
		return "", "", "", fmt.Errorf("aapt not found under %s", sdk)
	}
	return analyzer, builds[0], aapts[0], nil
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

/*
 * App icons live in the root of the top level .app bundle. Frameworks, plug-ins, Watch apps and Flutter
 * asset bundles also ship PNGs whose names contain "icon", and the previous descending-name sort picked
 * one of those, publishing artwork unrelated to the app icon.
 */
func findIPAIcon(entries []*zip.File, preferred []string) ([]byte, string) {
	return selectIcon(iconCandidates(entries, isAppBundleRootIcon), preferred)
}
func findAPKIcon(entries []*zip.File, declaredPaths []string) ([]byte, string) {
	paths := make(map[string]bool, len(declaredPaths))
	preferred := make([]string, 0, len(declaredPaths))
	for _, path := range declaredPaths {
		paths[path] = true
		preferred = append(preferred, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	return selectIcon(iconCandidates(entries, func(name string) bool { return paths[name] }), preferred)
}

type apkBadging struct {
	label string
	icons []string
}

var applicationBadging = regexp.MustCompile(`^application: label='(.*)' icon='(.*)'$`)

func parseAPKBadging(output []byte) apkBadging {
	var result apkBadging
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := badgingQuotedValue(line, "application-label:"); ok {
			result.label = value
			continue
		}
		if strings.HasPrefix(line, "application-icon-") {
			if colon := strings.IndexByte(line, ':'); colon >= 0 {
				if value, ok := badgingQuotedValue(line[colon:], ":"); ok {
					result.icons = append(result.icons, value)
				}
			}
			continue
		}
		if match := applicationBadging.FindStringSubmatch(line); len(match) == 3 {
			if result.label == "" {
				result.label = match[1]
			}
			if match[2] != "" {
				result.icons = append(result.icons, match[2])
			}
		}
	}
	return result
}

func badgingQuotedValue(line, prefix string) (string, bool) {
	value, ok := strings.CutPrefix(line, prefix)
	if !ok || len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		return "", false
	}
	return value[1 : len(value)-1], true
}
func isAppBundleRootIcon(name string) bool {
	parts := strings.Split(filepath.ToSlash(name), "/")
	if len(parts) != 3 || !strings.EqualFold(parts[0], "Payload") || !strings.HasSuffix(strings.ToLower(parts[1]), ".app") {
		return false
	}
	lower := strings.ToLower(parts[2])
	if !strings.Contains(lower, "appicon") && !strings.Contains(lower, "icon") {
		return false
	}
	return strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".webp")
}
func iconCandidates(entries []*zip.File, predicate func(string) bool) []*zip.File {
	result := make([]*zip.File, 0, 8)
	for _, file := range entries {
		if predicate(file.Name) && file.UncompressedSize64 <= 2<<20 {
			result = append(result, file)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if len(result) > 40 {
		result = result[:40]
	}
	return result
}
func imageFormat(data []byte) string {
	if len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return "image/jpeg"
	}
	return ""
}
func imageDimensions(data []byte, format string) (int, int) {
	switch format {
	case "image/png":
		// Walk the chunks: Xcode's CgBI variants insert a chunk before IHDR.
		for offset := 8; offset+8 <= len(data); {
			length := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			if string(data[offset+4:offset+8]) == "IHDR" && offset+16 <= len(data) {
				return int(binary.BigEndian.Uint32(data[offset+8 : offset+12])), int(binary.BigEndian.Uint32(data[offset+12 : offset+16]))
			}
			offset += 12 + length
		}
		return 0, 0
	case "image/webp":
		if len(data) < 30 {
			return 0, 0
		}
		switch string(data[12:16]) {
		case "VP8X":
			return 1 + int(data[24]) + int(data[25])<<8 + int(data[26])<<16, 1 + int(data[27]) + int(data[28])<<8 + int(data[29])<<16
		case "VP8 ":
			return int(binary.LittleEndian.Uint16(data[26:28]) & 0x3fff), int(binary.LittleEndian.Uint16(data[28:30]) & 0x3fff)
		case "VP8L":
			bits := binary.LittleEndian.Uint32(data[21:25])
			return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1
		}
	}
	return 0, 0
}

// Xcode rewrites app icon PNGs into Apple's CgBI variant, which browsers cannot decode.
func isAppleOptimizedPNG(data []byte) bool {
	return len(data) >= 16 && string(data[:8]) == "\x89PNG\r\n\x1a\n" && string(data[12:16]) == "CgBI"
}

/*
 * Icons copied straight out of a package may use formats a browser cannot render (CgBI PNG, WebP).
 * macOS `sips` decodes both, so store a plain PNG whenever that is possible.
 */
func (p *Parser) normalizeIcon(ctx context.Context, icon []byte, mime string) ([]byte, string) {
	if mime != "image/webp" && !isAppleOptimizedPNG(icon) {
		return icon, mime
	}
	directory, err := os.MkdirTemp("", "apphub-icon-*")
	if err != nil {
		return icon, mime
	}
	defer os.RemoveAll(directory)
	source := filepath.Join(directory, "icon."+strings.TrimPrefix(mime, "image/"))
	target := filepath.Join(directory, "icon.png")
	if err := os.WriteFile(source, icon, 0o600); err != nil {
		return icon, mime
	}
	if _, err := p.Runner.Run(ctx, "/usr/bin/sips", "-s", "format", "png", source, "--out", target); err != nil {
		return icon, mime
	}
	converted, err := os.ReadFile(target)
	if err != nil || !validPNG(converted) || isAppleOptimizedPNG(converted) || len(converted) > 2<<20 {
		return icon, mime
	}
	return converted, "image/png"
}

type iconChoice struct {
	name      string
	data      []byte
	mime      string
	preferred bool
	phone     bool
	round     bool
	area      int
	weight    int
}

func selectIcon(candidates []*zip.File, preferred []string) ([]byte, string) {
	var best *iconChoice
	for _, file := range candidates {
		input, err := file.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(input, 2<<20))
		input.Close()
		if err != nil {
			continue
		}
		format := imageFormat(data)
		if format == "" {
			continue
		}
		width, height := imageDimensions(data, format)
		lower := strings.ToLower(file.Name)
		choice := &iconChoice{name: file.Name, data: data, mime: format, area: width * height, weight: len(data),
			preferred: iconIsPreferred(file.Name, preferred),
			phone:     !strings.Contains(lower, "~ipad") && !strings.Contains(lower, "ipad"),
			round:     strings.Contains(lower, "round")}
		if best == nil || iconBeats(choice, best) {
			best = choice
		}
	}
	if best == nil {
		return nil, ""
	}
	return best.data, best.mime
}
func iconBeats(candidate, best *iconChoice) bool {
	if candidate.preferred != best.preferred {
		return candidate.preferred
	}
	if candidate.phone != best.phone {
		return candidate.phone
	}
	if candidate.round != best.round {
		return best.round
	}
	if candidate.area != best.area {
		return candidate.area > best.area
	}
	if candidate.weight != best.weight {
		return candidate.weight > best.weight
	}
	return candidate.name < best.name
}
func iconIsPreferred(name string, preferred []string) bool {
	base := strings.ToLower(filepath.Base(name))
	trimmed := strings.TrimSuffix(strings.TrimSuffix(base, ".png"), ".webp")
	trimmed = strings.TrimSuffix(strings.TrimSuffix(trimmed, "@2x"), "@3x")
	switch trimmed {
	case "ic_launcher", "launcher", "icon", "app_icon":
		return true
	}
	for _, value := range preferred {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.HasPrefix(trimmed, value) {
			return true
		}
	}
	return false
}
func (p *Parser) primaryIconNames(ctx context.Context, infoPath string) []string {
	names := make([]string, 0, 6)
	for _, key := range []string{"CFBundleIcons.CFBundlePrimaryIcon.CFBundleIconFiles", "CFBundleIcons~ipad.CFBundlePrimaryIcon.CFBundleIconFiles", "CFBundleIconFiles"} {
		raw := p.plutilJSON(ctx, infoPath, key)
		if raw == "" {
			continue
		}
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err != nil {
			continue
		}
		names = append(names, values...)
	}
	if name := p.plutil(ctx, infoPath, "CFBundleIconName"); name != "" {
		names = append(names, name)
	}
	return names
}
func readIcon(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 2<<20 {
		return nil, "", errors.New("icon exceeds 2 MB")
	}
	if format := imageFormat(data); format != "" {
		return data, format, nil
	}
	return nil, "", errors.New("icon must be PNG, JPEG or WebP")
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
