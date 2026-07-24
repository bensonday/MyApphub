package model

import "time"

const SchemaVersion = 1

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

type App struct {
	SchemaVersion     int                `json:"schemaVersion"`
	Slug              string             `json:"slug"`
	Platform          Platform           `json:"platform"`
	Identifier        string             `json:"identifier"`
	Name              string             `json:"name"`
	Icon              string             `json:"icon,omitempty"`
	CurrentVersionID  string             `json:"currentVersionId,omitempty"`
	SharePasswordHash string             `json:"sharePasswordHash,omitempty"`
	PasswordVersion   int                `json:"passwordVersion"`
	CreatedAt         time.Time          `json:"createdAt"`
	UpdatedAt         time.Time          `json:"updatedAt"`
	Versions          map[string]Version `json:"-"`
	StoragePath       string             `json:"-"`
}

type PublicApp struct {
	SchemaVersion     int       `json:"schemaVersion"`
	Slug              string    `json:"slug"`
	Platform          Platform  `json:"platform"`
	Identifier        string    `json:"identifier"`
	Name              string    `json:"name"`
	Icon              string    `json:"icon,omitempty"`
	CurrentVersionID  string    `json:"currentVersionId,omitempty"`
	PasswordProtected bool      `json:"passwordProtected"`
	PasswordVersion   int       `json:"passwordVersion,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	Versions          []Version `json:"versions,omitempty"`
}

type Version struct {
	SchemaVersion            int       `json:"schemaVersion"`
	ID                       string    `json:"id"`
	VersionName              string    `json:"versionName"`
	BuildNumber              string    `json:"buildNumber"`
	PackageFile              string    `json:"packageFile"`
	SHA256                   string    `json:"sha256"`
	FileSize                 int64     `json:"fileSize"`
	MinimumOSVersion         string    `json:"minimumOsVersion,omitempty"`
	DistributionType         string    `json:"distributionType,omitempty"`
	ProvisioningExpiresAt    time.Time `json:"provisioningExpiresAt,omitempty"`
	SigningCertificateSHA256 string    `json:"signingCertificateSha256,omitempty"`
	ReleaseNotes             string    `json:"releaseNotes,omitempty"`
	UploadedAt               time.Time `json:"uploadedAt"`
	PackagePath              string    `json:"-"`
}

type UploadMetadata struct {
	Platform                 Platform `json:"platform"`
	Identifier               string   `json:"identifier"`
	Name                     string   `json:"name"`
	VersionName              string   `json:"versionName"`
	BuildNumber              string   `json:"buildNumber"`
	MinimumOSVersion         string   `json:"minimumOsVersion,omitempty"`
	DistributionType         string   `json:"distributionType,omitempty"`
	ProvisioningExpiresAt    string   `json:"provisioningExpiresAt,omitempty"`
	SigningCertificateSHA256 string   `json:"signingCertificateSha256,omitempty"`
	ReleaseNotes             string   `json:"releaseNotes,omitempty"`
	Slug                     string   `json:"slug,omitempty"`
	Promote                  bool     `json:"promote"`
	SHA256                   string   `json:"sha256"`
	FileSize                 int64    `json:"fileSize"`
	IconMIME                 string   `json:"iconMime,omitempty"`
}

type UploadResult struct {
	App       PublicApp `json:"app"`
	Version   Version   `json:"version"`
	Duplicate bool      `json:"duplicate"`
}

type PublicRelease struct {
	App     PublicApp `json:"app"`
	Version Version   `json:"version"`
}

func (a App) Public(includeVersions bool) PublicApp {
	out := PublicApp{
		SchemaVersion: a.SchemaVersion, Slug: a.Slug, Platform: a.Platform,
		Identifier: a.Identifier, Name: a.Name, Icon: a.Icon,
		CurrentVersionID: a.CurrentVersionID, PasswordProtected: a.SharePasswordHash != "",
		PasswordVersion: a.PasswordVersion, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
	if includeVersions {
		out.Versions = make([]Version, 0, len(a.Versions))
		for _, version := range a.Versions {
			version.PackagePath = ""
			out.Versions = append(out.Versions, version)
		}
	}
	return out
}
