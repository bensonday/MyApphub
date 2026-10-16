package model

import "time"

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

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
	App         PublicApp `json:"app"`
	Version     Version   `json:"version"`
	Duplicate   bool      `json:"duplicate"`
	IconUpdated bool      `json:"iconUpdated"`
	NameUpdated bool      `json:"nameUpdated"`
}
