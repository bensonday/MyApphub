package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"myapphub/server/internal/config"
	"myapphub/server/internal/model"
)

func testStore(t *testing.T) (*Store, config.Config) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{RootDir: root, DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "runtime"), MaxPackageBytes: 1 << 20, DiskReserveBytes: 0, UploadTimeout: time.Minute, UDIDTTL: 30 * time.Minute, TrashRetention: 7 * 24 * time.Hour}
	repository := New(cfg)
	if err := repository.Init(); err != nil {
		t.Fatal(err)
	}
	return repository, cfg
}

func stage(t *testing.T, repository *Store, content []byte) (string, string) {
	t.Helper()
	file, err := repository.NewStagingFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(content)
	return file.Name(), hex.EncodeToString(hash[:])
}

func metadata(hash string, size int64, version, build string, promote bool) model.UploadMetadata {
	return model.UploadMetadata{Platform: model.PlatformAndroid, Identifier: "com.example.demo", Name: "Demo", VersionName: version, BuildNumber: build, Promote: promote, SHA256: hash, FileSize: size}
}

func TestCommitReloadDuplicateAndConflict(t *testing.T) {
	repository, cfg := testStore(t)
	content := []byte("PK\x03\x04first-package")
	path, hash := stage(t, repository, content)
	result, err := repository.CommitUpload(path, metadata(hash, int64(len(content)), "1.0.0", "1", true), int64(len(content)), hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.App.Slug != "demo" || result.App.CurrentVersionID != result.Version.ID {
		t.Fatalf("unexpected result: %+v", result)
	}
	if mode, _ := os.Stat(result.Version.PackagePath); mode != nil && mode.Mode().Perm() != 0o600 {
		t.Fatalf("package mode = %o", mode.Mode().Perm())
	}
	reloaded := New(cfg)
	if err := reloaded.Init(); err != nil {
		t.Fatal(err)
	}
	app, err := reloaded.App("demo")
	if err != nil || len(app.Versions) != 1 {
		t.Fatalf("reload failed: %v %+v", err, app)
	}
	duplicatePath, _ := stage(t, reloaded, content)
	duplicate, err := reloaded.CommitUpload(duplicatePath, metadata(hash, int64(len(content)), "1.0.0", "1", true), int64(len(content)), hash, nil)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate not detected: %v %+v", err, duplicate)
	}
	_ = os.Remove(duplicatePath)
	conflictContent := []byte("PK\x03\x04different")
	conflictPath, conflictHash := stage(t, reloaded, conflictContent)
	_, err = reloaded.CommitUpload(conflictPath, metadata(conflictHash, int64(len(conflictContent)), "1.0.0", "1", true), int64(len(conflictContent)), conflictHash, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("version conflict not rejected: %v", err)
	}
	_ = os.Remove(conflictPath)
}

func TestSameSlugSupportsIOSAndAndroid(t *testing.T) {
	repository, _ := testStore(t)
	androidContent := []byte("PK\x03\x04android")
	androidPath, androidHash := stage(t, repository, androidContent)
	android := metadata(androidHash, int64(len(androidContent)), "1.0", "1", true)
	android.Slug = "letsenglish"
	android.Identifier = "com.example.mobileapp"
	if _, err := repository.CommitUpload(androidPath, android, int64(len(androidContent)), androidHash, nil); err != nil {
		t.Fatal(err)
	}

	iosContent := []byte("PK\x03\x04ios")
	iosPath, iosHash := stage(t, repository, iosContent)
	ios := metadata(iosHash, int64(len(iosContent)), "1.0", "1", true)
	ios.Slug = "letsenglish"
	ios.Platform = model.PlatformIOS
	ios.Identifier = "com.example.mobileapp"
	ios.DistributionType = "enterprise"
	result, err := repository.CommitUpload(iosPath, ios, int64(len(iosContent)), iosHash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.App.Slug != "letsenglish" {
		t.Fatalf("iOS slug = %q", result.App.Slug)
	}
	apps := repository.AppsForSlug("letsenglish")
	if len(apps) != 2 || apps[0].Platform != model.PlatformIOS || apps[1].Platform != model.PlatformAndroid {
		t.Fatalf("unexpected variants: %+v", apps)
	}
}

func TestExplicitSlugRegroupsExistingPlatform(t *testing.T) {
	repository, _ := testStore(t)
	content := []byte("PK\x03\x04ios")
	path, hash := stage(t, repository, content)
	ios := metadata(hash, int64(len(content)), "1.0", "1", true)
	ios.Slug = "letsenglish-2"
	ios.Platform = model.PlatformIOS
	ios.Identifier = "com.example.mobileapp"
	ios.DistributionType = "enterprise"
	if _, err := repository.CommitUpload(path, ios, int64(len(content)), hash, nil); err != nil {
		t.Fatal(err)
	}

	duplicatePath, _ := stage(t, repository, content)
	ios.Slug = "letsenglish"
	result, err := repository.CommitUpload(duplicatePath, ios, int64(len(content)), hash, nil)
	if err != nil || !result.Duplicate || result.App.Slug != "letsenglish" {
		t.Fatalf("regroup failed: %v %+v", err, result)
	}
	if _, err := repository.AppPlatform("letsenglish", model.PlatformIOS); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AppPlatform("letsenglish-2", model.PlatformIOS); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old slug still active: %v", err)
	}
	_ = os.Remove(duplicatePath)
}

func TestDeleteAppMovesAllPlatformsToTrash(t *testing.T) {
	repository, cfg := testStore(t)
	for _, platform := range []model.Platform{model.PlatformAndroid, model.PlatformIOS} {
		content := []byte("PK\x03\x04" + string(platform))
		path, hash := stage(t, repository, content)
		value := metadata(hash, int64(len(content)), "1.0", "1", true)
		value.Slug = "letsenglish"
		value.Platform = platform
		value.Identifier = "com.example.mobileapp"
		if platform == model.PlatformIOS {
			value.DistributionType = "enterprise"
		}
		if _, err := repository.CommitUpload(path, value, int64(len(content)), hash, nil); err != nil {
			t.Fatal(err)
		}
	}
	until, err := repository.DeleteApp("letsenglish")
	if err != nil || until.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("delete app failed: %v %v", err, until)
	}
	if apps := repository.AppsForSlug("letsenglish"); len(apps) != 0 {
		t.Fatalf("app still loaded: %+v", apps)
	}
	trash, err := os.ReadDir(filepath.Join(cfg.DataDir, ".trash"))
	if err != nil || len(trash) != 2 {
		t.Fatalf("trash entries: %v %+v", err, trash)
	}
	for _, entry := range trash {
		if _, err := os.Stat(filepath.Join(cfg.DataDir, ".trash", entry.Name(), ".trash-app.json")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVersionLifecycleAndPassword(t *testing.T) {
	repository, _ := testStore(t)
	first := []byte("PK\x03\x04first")
	firstPath, firstHash := stage(t, repository, first)
	one, err := repository.CommitUpload(firstPath, metadata(firstHash, int64(len(first)), "1.0.0", "1", true), int64(len(first)), firstHash, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := []byte("PK\x03\x04second")
	secondPath, secondHash := stage(t, repository, second)
	two, err := repository.CommitUpload(secondPath, metadata(secondHash, int64(len(second)), "2.0.0", "2", false), int64(len(second)), secondHash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if two.App.CurrentVersionID != one.Version.ID {
		t.Fatal("--no-promote changed current version")
	}
	if _, err := repository.Promote(one.App.Slug, two.Version.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteVersion(one.App.Slug, two.Version.ID); !errors.Is(err, ErrCurrentVersion) {
		t.Fatalf("current version deletion not blocked: %v", err)
	}
	if _, err := repository.Promote(one.App.Slug, one.Version.ID); err != nil {
		t.Fatal(err)
	}
	until, err := repository.DeleteVersion(one.App.Slug, two.Version.ID)
	if err != nil || until.Before(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("delete failed: %v %v", err, until)
	}
	if _, err := repository.RestoreVersion(one.App.Slug, two.Version.ID); err != nil {
		t.Fatal(err)
	}
	protected, err := repository.SetPassword(one.App.Slug, "correct horse battery staple")
	if err != nil || !protected.PasswordProtected || protected.PasswordVersion != 1 {
		t.Fatalf("password update failed: %v %+v", err, protected)
	}
	cleared, err := repository.SetPassword(one.App.Slug, "")
	if err != nil || cleared.PasswordProtected || cleared.PasswordVersion != 2 {
		t.Fatalf("password clear failed: %v %+v", err, cleared)
	}
}

func TestValidationAndIcon(t *testing.T) {
	repository, _ := testStore(t)
	content := []byte("PK\x03\x04data")
	path, hash := stage(t, repository, content)
	value := metadata(hash, int64(len(content)), "1.0", "1", true)
	value.Slug = "../escape"
	if _, err := repository.CommitUpload(path, value, int64(len(content)), hash, nil); err == nil {
		t.Fatal("unsafe slug accepted")
	}
	_ = os.Remove(path)
	path, hash = stage(t, repository, content)
	value = metadata(hash, int64(len(content)), "1.0", "1", true)
	value.IconMIME = "image/png"
	if _, err := repository.CommitUpload(path, value, int64(len(content)), hash, []byte("not-png")); err == nil {
		t.Fatal("invalid icon accepted")
	}
	_ = os.Remove(path)
	path, hash = stage(t, repository, content)
	value = metadata(hash, int64(len(content)), "1.0", "1", true)
	value.Platform = model.PlatformIOS
	value.DistributionType = "app-store"
	if _, err := repository.CommitUpload(path, value, int64(len(content)), hash, nil); err == nil {
		t.Fatal("App Store IPA accepted")
	}
	_ = os.Remove(path)
}

func TestForeignLockIsNotRemoved(t *testing.T) {
	repository, _ := testStore(t)
	if err := os.WriteFile(repository.lockPath, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repository.withWriteLock(func() error { return nil }); !errors.Is(err, ErrWriteBusy) {
		t.Fatalf("foreign lock was ignored: %v", err)
	}
	if _, err := os.Stat(repository.lockPath); err != nil {
		t.Fatal("foreign lock was removed")
	}
}

func TestCleanupAndDamagedRecords(t *testing.T) {
	repository, cfg := testStore(t)
	stale := filepath.Join(repository.stagingDir, "old")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-3 * time.Minute)
	_ = os.Chtimes(stale, old, old)
	if err := repository.Cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale staging file not removed")
	}
	damaged := filepath.Join(cfg.DataDir, "apps", "damaged")
	_ = os.MkdirAll(damaged, 0o700)
	_ = os.WriteFile(filepath.Join(damaged, "app.json"), []byte("{"), 0o600)
	if err := repository.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.App("damaged"); !errors.Is(err, ErrNotFound) {
		t.Fatal("damaged app was loaded")
	}
}

func TestAtomicJSONLeavesValidFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "record.json")
	if err := atomicJSON(path, map[string]string{"value": strings.Repeat("x", 1024)}); err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	if err := readJSON(path, &value); err != nil || len(value["value"]) != 1024 {
		t.Fatalf("invalid atomic JSON: %v", err)
	}
}

func BenchmarkAppsListTwentyThousandVersions(b *testing.B) {
	repository := &Store{apps: make(map[string]*model.App), identities: make(map[string]string)}
	for appIndex := 0; appIndex < 100; appIndex++ {
		slug := fmt.Sprintf("app-%03d", appIndex)
		app := &model.App{SchemaVersion: 1, Slug: slug, Platform: model.PlatformAndroid, Identifier: "com.example." + slug, Name: slug, Versions: make(map[string]model.Version, 200)}
		for versionIndex := 0; versionIndex < 200; versionIndex++ {
			id := fmt.Sprintf("1.0-%03d-aabbccdd", versionIndex)
			app.Versions[id] = model.Version{ID: id, VersionName: "1.0", BuildNumber: fmt.Sprint(versionIndex), UploadedAt: time.Unix(int64(versionIndex), 0)}
		}
		repository.apps[slug] = app
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if len(repository.Apps()) != 100 {
			b.Fatal("missing apps")
		}
	}
}
