package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"myapphub/server/internal/auth"
	"myapphub/server/internal/config"
	"myapphub/server/internal/model"
)

var (
	safeSegment       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,254}$`)
)

var (
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("version conflict")
	ErrCurrentVersion = errors.New("current version cannot be deleted")
	ErrWriteBusy      = errors.New("another write operation is in progress")
)

type Store struct {
	cfg        config.Config
	appsDir    string
	stagingDir string
	trashDir   string
	udidDir    string
	lockPath   string
	mu         sync.RWMutex
	writeMu    sync.Mutex
	apps       map[string]*model.App
	identities map[string]string
}

func New(cfg config.Config) *Store {
	return &Store{
		cfg: cfg, appsDir: filepath.Join(cfg.DataDir, "apps"), stagingDir: filepath.Join(cfg.DataDir, ".staging"),
		trashDir: filepath.Join(cfg.DataDir, ".trash"), udidDir: filepath.Join(cfg.RuntimeDir, "udid"),
		lockPath: filepath.Join(cfg.DataDir, ".write.lock"), apps: make(map[string]*model.App), identities: make(map[string]string),
	}
}

func (s *Store) Init() error {
	for _, dir := range []string{s.appsDir, s.stagingDir, s.trashDir, s.udidDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if err := s.Cleanup(time.Now()); err != nil {
		return err
	}
	return s.Reload()
}

func (s *Store) Reload() error {
	entries, err := os.ReadDir(s.appsDir)
	if err != nil {
		return err
	}
	apps := make(map[string]*model.App)
	identities := make(map[string]string)
	loadApp := func(appDir string) {
		var app model.App
		if err := readJSON(filepath.Join(appDir, "app.json"), &app); err != nil || app.SchemaVersion != model.SchemaVersion || ValidateSegment(app.Slug) != nil {
			return
		}
		app.StoragePath = appDir
		app.Versions = make(map[string]model.Version)
		versionEntries, _ := os.ReadDir(filepath.Join(appDir, "versions"))
		for _, versionEntry := range versionEntries {
			if !versionEntry.IsDir() || ValidateSegment(versionEntry.Name()) != nil {
				continue
			}
			versionDir := filepath.Join(appDir, "versions", versionEntry.Name())
			var version model.Version
			if err := readJSON(filepath.Join(versionDir, "version.json"), &version); err != nil || version.SchemaVersion != model.SchemaVersion || version.ID != versionEntry.Name() {
				continue
			}
			packagePath := filepath.Join(versionDir, version.PackageFile)
			if _, err := os.Stat(packagePath); err != nil {
				continue
			}
			version.PackagePath = packagePath
			app.Versions[version.ID] = version
		}
		if app.CurrentVersionID != "" {
			if _, ok := app.Versions[app.CurrentVersionID]; !ok {
				app.CurrentVersionID = ""
			}
		}
		copy := app
		apps[appKey(app.Slug, app.Platform)] = &copy
		identities[identity(app.Platform, app.Identifier)] = appKey(app.Slug, app.Platform)
	}
	for _, entry := range entries {
		if !entry.IsDir() || ValidateSegment(entry.Name()) != nil {
			continue
		}
		appDir := filepath.Join(s.appsDir, entry.Name())
		loadApp(appDir) // v1 layout: apps/{slug}/app.json
		for _, platform := range []model.Platform{model.PlatformIOS, model.PlatformAndroid} {
			loadApp(filepath.Join(appDir, string(platform)))
		}
	}
	s.mu.Lock()
	s.apps = apps
	s.identities = identities
	s.mu.Unlock()
	return nil
}

func (s *Store) Apps() []model.PublicApp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.PublicApp, 0, len(s.apps))
	for _, app := range s.apps {
		item := app.Public(true)
		sort.Slice(item.Versions, func(i, j int) bool { return item.Versions[i].UploadedAt.After(item.Versions[j].UploadedAt) })
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Slug == result[j].Slug {
			return result[i].Platform < result[j].Platform
		}
		return result[i].Slug < result[j].Slug
	})
	return result
}

func (s *Store) App(slug string) (model.App, error) {
	apps := s.AppsForSlug(slug)
	if len(apps) == 0 {
		return model.App{}, ErrNotFound
	}
	return apps[0], nil
}

func (s *Store) AppPlatform(slug string, platform model.Platform) (model.App, error) {
	if err := ValidateSegment(slug); err != nil {
		return model.App{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, ok := s.apps[appKey(slug, platform)]
	if !ok {
		return model.App{}, ErrNotFound
	}
	return cloneApp(*app), nil
}

func (s *Store) AppsForSlug(slug string) []model.App {
	if ValidateSegment(slug) != nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]model.App, 0, 2)
	for _, platform := range []model.Platform{model.PlatformIOS, model.PlatformAndroid} {
		if app, ok := s.apps[appKey(slug, platform)]; ok {
			result = append(result, cloneApp(*app))
		}
	}
	return result
}

func (s *Store) AppForVersion(slug, versionID string) (model.App, error) {
	for _, app := range s.AppsForSlug(slug) {
		if _, ok := app.Versions[versionID]; ok {
			return app, nil
		}
	}
	return model.App{}, ErrNotFound
}

func (s *Store) NewStagingFile() (*os.File, error) {
	return os.CreateTemp(s.stagingDir, "upload-*.package")
}

func (s *Store) AvailableBytes() (uint64, error) {
	var info syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.DataDir, &info); err != nil {
		return 0, err
	}
	return info.Bavail * uint64(info.Bsize), nil
}

func (s *Store) CommitUpload(stagingPath string, metadata model.UploadMetadata, actualSize int64, actualSHA string, icon []byte) (model.UploadResult, error) {
	if err := validateMetadata(metadata); err != nil {
		return model.UploadResult{}, err
	}
	if err := validateIcon(metadata.IconMIME, icon); err != nil {
		return model.UploadResult{}, err
	}
	if actualSize != metadata.FileSize {
		return model.UploadResult{}, fmt.Errorf("file size mismatch")
	}
	if !strings.EqualFold(actualSHA, metadata.SHA256) {
		return model.UploadResult{}, fmt.Errorf("sha256 mismatch")
	}
	if actualSize < 4 || actualSize > s.cfg.MaxPackageBytes {
		return model.UploadResult{}, fmt.Errorf("package size is not allowed")
	}
	var result model.UploadResult
	err := s.withWriteLock(func() error {
		s.mu.RLock()
		key := s.identities[identity(metadata.Platform, metadata.Identifier)]
		slug := ""
		var existing *model.App
		if key != "" {
			copy := cloneApp(*s.apps[key])
			existing = &copy
			slug = copy.Slug
		}
		s.mu.RUnlock()
		if existing != nil && metadata.Slug != "" && metadata.Slug != existing.Slug {
			s.mu.RLock()
			_, occupied := s.apps[appKey(metadata.Slug, metadata.Platform)]
			s.mu.RUnlock()
			if occupied {
				return ErrConflict
			}
			existing.Slug = metadata.Slug
			if peers := s.AppsForSlug(metadata.Slug); len(peers) > 0 {
				existing.SharePasswordHash = peers[0].SharePasswordHash
				existing.PasswordVersion = peers[0].PasswordVersion
			}
			existing.UpdatedAt = time.Now().UTC()
			if err := atomicJSON(filepath.Join(existing.StoragePath, "app.json"), existing); err != nil {
				return err
			}
			if err := s.Reload(); err != nil {
				return err
			}
			slug = metadata.Slug
			regrouped, err := s.AppPlatform(slug, metadata.Platform)
			if err != nil {
				return err
			}
			existing = &regrouped
		}
		if existing != nil {
			for _, version := range existing.Versions {
				if strings.EqualFold(version.SHA256, actualSHA) {
					result = model.UploadResult{App: existing.Public(true), Version: version, Duplicate: true}
					return nil
				}
				if version.VersionName == metadata.VersionName && version.BuildNumber == metadata.BuildNumber {
					return ErrConflict
				}
			}
		} else {
			base := metadata.Slug
			if base == "" {
				base = slugify(metadata.Name)
			}
			slug = base
			s.mu.RLock()
			_, occupied := s.apps[appKey(slug, metadata.Platform)]
			s.mu.RUnlock()
			for n := 2; occupied; n++ {
				slug = fmt.Sprintf("%s-%d", base, n)
				s.mu.RLock()
				_, occupied = s.apps[appKey(slug, metadata.Platform)]
				s.mu.RUnlock()
			}
		}
		versionID := fmt.Sprintf("%s-%s-%s", slugify(metadata.VersionName), slugify(metadata.BuildNumber), strings.ToLower(actualSHA[:8]))
		if err := ValidateSegment(versionID); err != nil {
			return err
		}
		appDir := filepath.Join(s.appsDir, slug, string(metadata.Platform))
		if existing != nil {
			appDir = existing.StoragePath
		}
		versionDir := filepath.Join(appDir, "versions", versionID)
		if _, err := os.Stat(versionDir); err == nil {
			return ErrConflict
		}
		if err := os.MkdirAll(versionDir, 0o700); err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				_ = os.RemoveAll(versionDir)
			}
		}()
		extension := ".apk"
		if metadata.Platform == model.PlatformIOS {
			extension = ".ipa"
		}
		packageFile := "package" + extension
		packagePath := filepath.Join(versionDir, packageFile)
		if err := os.Rename(stagingPath, packagePath); err != nil {
			return err
		}
		if err := os.Chmod(packagePath, 0o600); err != nil {
			return err
		}
		now := time.Now().UTC()
		version := model.Version{SchemaVersion: model.SchemaVersion, ID: versionID, VersionName: metadata.VersionName,
			BuildNumber: metadata.BuildNumber, PackageFile: packageFile, SHA256: strings.ToLower(actualSHA), FileSize: actualSize,
			MinimumOSVersion: metadata.MinimumOSVersion, DistributionType: metadata.DistributionType,
			SigningCertificateSHA256: metadata.SigningCertificateSHA256, ReleaseNotes: metadata.ReleaseNotes, UploadedAt: now, PackagePath: packagePath}
		if metadata.ProvisioningExpiresAt != "" {
			parsed, err := time.Parse(time.RFC3339, metadata.ProvisioningExpiresAt)
			if err != nil {
				return fmt.Errorf("invalid provisioning expiry: %w", err)
			}
			version.ProvisioningExpiresAt = parsed
		}
		if err := atomicJSON(filepath.Join(versionDir, "version.json"), version); err != nil {
			return err
		}
		iconFile := ""
		if existing != nil {
			iconFile = existing.Icon
		}
		if len(icon) > 0 {
			extension := ".png"
			if metadata.IconMIME == "image/jpeg" {
				extension = ".jpg"
			}
			iconFile = "icon" + extension
			if err := atomicBytes(filepath.Join(appDir, iconFile), icon, 0o600); err != nil {
				return err
			}
		}
		app := model.App{SchemaVersion: model.SchemaVersion, Slug: slug, Platform: metadata.Platform,
			Identifier: metadata.Identifier, Name: metadata.Name, Icon: iconFile, PasswordVersion: 0,
			CreatedAt: now, UpdatedAt: now, Versions: map[string]model.Version{versionID: version}}
		if existing == nil {
			if peers := s.AppsForSlug(slug); len(peers) > 0 {
				app.SharePasswordHash = peers[0].SharePasswordHash
				app.PasswordVersion = peers[0].PasswordVersion
			}
		}
		if existing != nil {
			app.SharePasswordHash = existing.SharePasswordHash
			app.PasswordVersion = existing.PasswordVersion
			app.CreatedAt = existing.CreatedAt
			app.CurrentVersionID = existing.CurrentVersionID
			app.Versions = existing.Versions
			app.Versions[versionID] = version
		}
		if metadata.Promote {
			app.CurrentVersionID = versionID
		}
		if err := atomicJSON(filepath.Join(appDir, "app.json"), app); err != nil {
			return err
		}
		committed = true
		if err := s.Reload(); err != nil {
			return err
		}
		loaded, _ := s.AppPlatform(slug, metadata.Platform)
		result = model.UploadResult{App: loaded.Public(true), Version: loaded.Versions[versionID]}
		return nil
	})
	return result, err
}

func (s *Store) Promote(slug, versionID string) (model.PublicApp, error) {
	var result model.PublicApp
	err := s.withWriteLock(func() error {
		app, err := s.AppForVersion(slug, versionID)
		if err != nil {
			return err
		}
		if err := ValidateSegment(versionID); err != nil {
			return err
		}
		if _, ok := app.Versions[versionID]; !ok {
			return ErrNotFound
		}
		app.CurrentVersionID = versionID
		app.UpdatedAt = time.Now().UTC()
		if err := atomicJSON(filepath.Join(app.StoragePath, "app.json"), app); err != nil {
			return err
		}
		if err := s.Reload(); err != nil {
			return err
		}
		loaded, _ := s.AppPlatform(slug, app.Platform)
		result = loaded.Public(true)
		return nil
	})
	return result, err
}

func (s *Store) SetPassword(slug, password string) (model.PublicApp, error) {
	var result model.PublicApp
	err := s.withWriteLock(func() error {
		apps := s.AppsForSlug(slug)
		if len(apps) == 0 {
			return ErrNotFound
		}
		hash := ""
		if password != "" {
			var hashErr error
			hash, hashErr = auth.HashPassword(password)
			if hashErr != nil {
				return hashErr
			}
		}
		for index := range apps {
			apps[index].SharePasswordHash = hash
			apps[index].PasswordVersion++
			apps[index].UpdatedAt = time.Now().UTC()
			if err := atomicJSON(filepath.Join(apps[index].StoragePath, "app.json"), apps[index]); err != nil {
				return err
			}
		}
		if err := s.Reload(); err != nil {
			return err
		}
		loaded, _ := s.AppPlatform(slug, apps[0].Platform)
		result = loaded.Public(false)
		return nil
	})
	return result, err
}

func (s *Store) DeleteApp(slug string) (time.Time, error) {
	var recoverable time.Time
	err := s.withWriteLock(func() error {
		apps := s.AppsForSlug(slug)
		if len(apps) == 0 {
			return ErrNotFound
		}
		deleted := time.Now().UTC()
		type move struct{ from, to string }
		moves := make([]move, 0)
		targets := make([]string, 0, len(apps))
		rollback := func() {
			for index := len(moves) - 1; index >= 0; index-- {
				_ = os.MkdirAll(filepath.Dir(moves[index].from), 0o700)
				_ = os.Rename(moves[index].to, moves[index].from)
			}
			for _, target := range targets {
				_ = os.RemoveAll(target)
			}
		}
		for _, app := range apps {
			target := filepath.Join(s.trashDir, fmt.Sprintf("app--%s--%s--%d", slug, app.Platform, deleted.UnixMilli()))
			if err := os.MkdirAll(target, 0o700); err != nil {
				rollback()
				return err
			}
			targets = append(targets, target)
			entries, err := os.ReadDir(app.StoragePath)
			if err != nil {
				rollback()
				return err
			}
			for _, entry := range entries {
				if entry.IsDir() && (entry.Name() == string(model.PlatformIOS) || entry.Name() == string(model.PlatformAndroid)) {
					continue
				}
				from := filepath.Join(app.StoragePath, entry.Name())
				to := filepath.Join(target, entry.Name())
				if err := os.Rename(from, to); err != nil {
					rollback()
					return err
				}
				moves = append(moves, move{from: from, to: to})
			}
			storageRelative, err := filepath.Rel(s.appsDir, app.StoragePath)
			if err != nil || strings.HasPrefix(storageRelative, "..") {
				rollback()
				return fmt.Errorf("invalid app storage path")
			}
			metadata := map[string]any{"slug": slug, "platform": app.Platform, "storagePath": storageRelative, "deletedAt": deleted}
			if err := atomicJSON(filepath.Join(target, ".trash-app.json"), metadata); err != nil {
				rollback()
				return err
			}
		}
		for _, app := range apps {
			_ = os.Remove(app.StoragePath)
			_ = os.Remove(filepath.Dir(app.StoragePath))
		}
		if err := s.Reload(); err != nil {
			rollback()
			return err
		}
		recoverable = deleted.Add(s.cfg.TrashRetention)
		return nil
	})
	return recoverable, err
}

func (s *Store) DeleteVersion(slug, versionID string) (time.Time, error) {
	var recoverable time.Time
	err := s.withWriteLock(func() error {
		app, err := s.AppForVersion(slug, versionID)
		if err != nil {
			return err
		}
		if err := ValidateSegment(versionID); err != nil {
			return err
		}
		if app.CurrentVersionID == versionID {
			return ErrCurrentVersion
		}
		if _, ok := app.Versions[versionID]; !ok {
			return ErrNotFound
		}
		deleted := time.Now().UTC()
		target := filepath.Join(s.trashDir, fmt.Sprintf("%s--%s--%s--%d", slug, app.Platform, versionID, deleted.UnixMilli()))
		if err := os.Rename(filepath.Join(app.StoragePath, "versions", versionID), target); err != nil {
			return err
		}
		if err := atomicJSON(filepath.Join(target, ".trash.json"), map[string]any{"slug": slug, "platform": app.Platform, "versionId": versionID, "deletedAt": deleted}); err != nil {
			return err
		}
		if err := s.Reload(); err != nil {
			return err
		}
		recoverable = deleted.Add(s.cfg.TrashRetention)
		return nil
	})
	return recoverable, err
}

func (s *Store) RestoreVersion(slug, versionID string) (model.PublicApp, error) {
	var result model.PublicApp
	err := s.withWriteLock(func() error {
		if _, err := s.AppForVersion(slug, versionID); err == nil {
			return ErrConflict
		}
		entries, err := os.ReadDir(s.trashDir)
		if err != nil {
			return err
		}
		prefixes := []string{slug + "--" + versionID + "--", slug + "--ios--" + versionID + "--", slug + "--android--" + versionID + "--"}
		names := make([]string, 0)
		for _, entry := range entries {
			if entry.IsDir() {
				for _, prefix := range prefixes {
					if strings.HasPrefix(entry.Name(), prefix) {
						names = append(names, entry.Name())
						break
					}
				}
			}
		}
		if len(names) == 0 {
			return ErrNotFound
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		source := filepath.Join(s.trashDir, names[0])
		var trash struct {
			Platform model.Platform `json:"platform"`
		}
		_ = readJSON(filepath.Join(source, ".trash.json"), &trash)
		if trash.Platform == "" {
			parts := strings.Split(names[0], "--")
			if len(parts) >= 4 {
				trash.Platform = model.Platform(parts[1])
			}
			if trash.Platform == "" {
				fallback, fallbackErr := s.App(slug)
				if fallbackErr != nil {
					return fallbackErr
				}
				trash.Platform = fallback.Platform
			}
		}
		app, err := s.AppPlatform(slug, trash.Platform)
		if err != nil {
			return err
		}
		_ = os.Remove(filepath.Join(source, ".trash.json"))
		if err := os.Rename(source, filepath.Join(app.StoragePath, "versions", versionID)); err != nil {
			return err
		}
		if err := s.Reload(); err != nil {
			return err
		}
		loaded, _ := s.AppPlatform(slug, app.Platform)
		result = loaded.Public(true)
		return nil
	})
	return result, err
}

func (s *Store) Cleanup(now time.Time) error {
	for _, item := range []struct {
		dir string
		age time.Duration
	}{{s.stagingDir, 2 * s.cfg.UploadTimeout}, {s.udidDir, s.cfg.UDIDTTL}, {s.trashDir, s.cfg.TrashRetention}} {
		entries, _ := os.ReadDir(item.dir)
		for _, entry := range entries {
			info, err := entry.Info()
			if err == nil && now.Sub(info.ModTime()) > item.age {
				_ = os.RemoveAll(filepath.Join(item.dir, entry.Name()))
			}
		}
	}
	return nil
}

func (s *Store) UDIDDir() string { return s.udidDir }
func (s *Store) AppsDir() string { return s.appsDir }

func (s *Store) withWriteLock(action func() error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	file, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		info, statErr := os.Stat(s.lockPath)
		if statErr == nil && time.Since(info.ModTime()) > 2*s.cfg.UploadTimeout {
			_ = os.Remove(s.lockPath)
			file, err = os.OpenFile(s.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		}
	}
	if errors.Is(err, fs.ErrExist) {
		return ErrWriteBusy
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Close()
	defer os.Remove(s.lockPath)
	return action()
}

func ValidateSegment(value string) error {
	if !safeSegment.MatchString(value) || strings.Contains(value, "..") {
		return fmt.Errorf("invalid path segment")
	}
	return nil
}

func validateMetadata(value model.UploadMetadata) error {
	if value.Platform != model.PlatformIOS && value.Platform != model.PlatformAndroid {
		return fmt.Errorf("unsupported platform")
	}
	if !identifierPattern.MatchString(value.Identifier) {
		return fmt.Errorf("invalid identifier")
	}
	if len(value.Name) == 0 || len(value.Name) > 100 {
		return fmt.Errorf("invalid app name")
	}
	if ValidateSegment(value.VersionName) != nil || ValidateSegment(value.BuildNumber) != nil {
		return fmt.Errorf("invalid version")
	}
	if !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(value.SHA256) {
		return fmt.Errorf("invalid sha256")
	}
	if value.Platform == model.PlatformIOS && value.DistributionType != "enterprise" && value.DistributionType != "ad-hoc" {
		return fmt.Errorf("unsupported iOS distribution type")
	}
	if value.Slug != "" {
		if err := ValidateSegment(value.Slug); err != nil {
			return err
		}
	}
	if len(value.ReleaseNotes) > 10_000 {
		return fmt.Errorf("release notes are too long")
	}
	return nil
}

func validateIcon(mime string, data []byte) error {
	if len(data) == 0 {
		if mime != "" {
			return fmt.Errorf("icon MIME provided without icon data")
		}
		return nil
	}
	if len(data) > 2<<20 {
		return fmt.Errorf("icon is too large")
	}
	png := len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n"
	jpeg := len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff
	if mime == "image/png" && png || mime == "image/jpeg" && jpeg {
		return nil
	}
	return fmt.Errorf("invalid icon content")
}

func identity(platform model.Platform, identifier string) string {
	return string(platform) + ":" + identifier
}
func appKey(slug string, platform model.Platform) string { return slug + "\x00" + string(platform) }
func cloneApp(app model.App) model.App {
	copy := app
	copy.Versions = make(map[string]model.Version, len(app.Versions))
	for key, value := range app.Versions {
		copy.Versions[key] = value
	}
	return copy
}

func slugify(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	dash := false
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			builder.WriteRune(char)
			dash = false
		} else if builder.Len() > 0 && !dash {
			builder.WriteByte('-')
			dash = true
		}
	}
	result := strings.Trim(builder.String(), "-")
	if len(result) > 64 {
		result = result[:64]
	}
	if result == "" {
		bytes := make([]byte, 4)
		_, _ = rand.Read(bytes)
		result = "app-" + hex.EncodeToString(bytes)
	}
	return result
}

func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicBytes(path, data, 0o600)
}

func atomicBytes(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
