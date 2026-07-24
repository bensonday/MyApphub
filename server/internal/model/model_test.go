package model

import (
	"testing"
	"time"
)

func TestPublicAppProjection(t *testing.T) {
	now := time.Now()
	app := App{SchemaVersion: 1, Slug: "demo", Platform: PlatformIOS, Identifier: "com.example.demo", Name: "Demo", CurrentVersionID: "v1", SharePasswordHash: "secret-hash", PasswordVersion: 2, CreatedAt: now, UpdatedAt: now, Versions: map[string]Version{"v1": {ID: "v1", PackagePath: "/private/package.ipa"}}}
	public := app.Public(true)
	if !public.PasswordProtected || public.PasswordVersion != 2 || len(public.Versions) != 1 {
		t.Fatalf("unexpected public app: %+v", public)
	}
	if public.Versions[0].PackagePath != "" {
		t.Fatal("private package path leaked")
	}
	withoutVersions := app.Public(false)
	if withoutVersions.Versions != nil {
		t.Fatal("versions included unexpectedly")
	}
}
