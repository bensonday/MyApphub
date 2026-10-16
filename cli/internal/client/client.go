package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"myapphub/cli/internal/model"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Message }

func New(baseURL, token string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid APPHUB_SERVER")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" {
		return nil, errors.New("APPHUB_SERVER must use HTTPS")
	}
	if len(token) < 32 {
		return nil, errors.New("APPHUB_TOKEN must contain at least 32 bytes")
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) List(ctx context.Context) ([]model.PublicApp, error) {
	var response struct {
		Apps []model.PublicApp `json:"apps"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/apps", nil, &response); err != nil {
		return nil, err
	}
	return response.Apps, nil
}

func (c *Client) Upload(ctx context.Context, packagePath string, metadata model.UploadMetadata, icon []byte, progress io.Writer) (model.UploadResult, error) {
	file, err := os.Open(packagePath)
	if err != nil {
		return model.UploadResult{}, err
	}
	defer file.Close()
	var started struct {
		ID          string          `json:"id"`
		PartSize    int             `json:"partSize"`
		Duplicate   bool            `json:"duplicate"`
		NameUpdated bool            `json:"nameUpdated"`
		App         model.PublicApp `json:"app"`
		Version     model.Version   `json:"version"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/uploads", metadata, &started); err != nil {
		return model.UploadResult{}, err
	}
	if started.Duplicate {
		/*
		 * The package itself is already stored, but the icon may have been extracted wrongly by an older
		 * build, so a corrected icon is still pushed to the existing app.
		 */
		iconUpdated := false
		if len(icon) > 0 && started.App.Slug != "" {
			iconPath := fmt.Sprintf("/api/v1/apps/%s/%s/icon", url.PathEscape(started.App.Slug), url.PathEscape(string(metadata.Platform)))
			if err := c.putBytes(ctx, iconPath, icon, ""); err != nil {
				return model.UploadResult{}, err
			}
			iconUpdated = true
		}
		return model.UploadResult{App: started.App, Version: started.Version, Duplicate: true, IconUpdated: iconUpdated, NameUpdated: started.NameUpdated}, nil
	}
	if started.ID == "" || started.PartSize < 5<<20 || started.PartSize > 32<<20 {
		return model.UploadResult{}, errors.New("server returned an invalid upload session")
	}
	uploadPath := "/api/v1/uploads/" + url.PathEscape(started.ID)
	completed := false
	defer func() {
		if !completed {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = c.doJSON(cancelCtx, http.MethodDelete, uploadPath, nil, &map[string]any{})
		}
	}()
	if len(icon) > 0 {
		if err := c.putBytes(ctx, uploadPath+"/icon", icon, ""); err != nil {
			return model.UploadResult{}, err
		}
	}
	buffer := make([]byte, started.PartSize)
	type uploadedPart struct {
		PartNumber int    `json:"partNumber"`
		ETag       string `json:"etag"`
	}
	parts := make([]uploadedPart, 0, (metadata.FileSize+int64(started.PartSize)-1)/int64(started.PartSize))
	var sent int64
	for number := 1; ; number++ {
		count, readErr := io.ReadFull(file, buffer)
		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != io.ErrUnexpectedEOF {
			return model.UploadResult{}, readErr
		}
		digest := sha256.Sum256(buffer[:count])
		var part uploadedPart
		if err := c.putBytesResult(ctx, fmt.Sprintf("%s/parts/%d", uploadPath, number), buffer[:count], fmt.Sprintf("%x", digest), &part); err != nil {
			return model.UploadResult{}, err
		}
		if part.PartNumber != number || part.ETag == "" {
			return model.UploadResult{}, errors.New("server returned an invalid upload part")
		}
		parts = append(parts, part)
		sent += int64(count)
		if progress != nil {
			_, _ = fmt.Fprintf(progress, "\r上传进度 %.1f%%", 100*float64(sent)/float64(metadata.FileSize))
		}
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if sent != metadata.FileSize {
		return model.UploadResult{}, errors.New("package size changed during upload")
	}
	if progress != nil {
		_, _ = fmt.Fprintln(progress)
	}
	var result model.UploadResult
	if err := c.doJSON(ctx, http.MethodPost, uploadPath+"/complete", map[string]any{"parts": parts}, &result); err != nil {
		return model.UploadResult{}, err
	}
	completed = true
	return result, nil
}

func (c *Client) putBytes(ctx context.Context, requestPath string, data []byte, checksum string) error {
	return c.putBytesResult(ctx, requestPath, data, checksum, &map[string]any{})
}

func (c *Client) putBytesResult(ctx context.Context, requestPath string, data []byte, checksum string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+requestPath, bytes.NewReader(data))
	if err != nil {
		return err
	}
	c.authorize(request)
	request.Header.Set("Content-Type", "application/octet-stream")
	if checksum != "" {
		request.Header.Set("X-Part-SHA256", checksum)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decodeResponse(response, target)
}

func (c *Client) Promote(ctx context.Context, slug, version string) (model.PublicApp, error) {
	var response struct {
		App model.PublicApp `json:"app"`
	}
	err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/apps/%s/versions/%s/promote", url.PathEscape(slug), url.PathEscape(version)), nil, &response)
	return response.App, err
}
func (c *Client) Delete(ctx context.Context, slug, version string) (time.Time, error) {
	var response struct {
		RecoverableUntil time.Time `json:"recoverableUntil"`
	}
	err := c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/apps/%s/versions/%s", url.PathEscape(slug), url.PathEscape(version)), nil, &response)
	return response.RecoverableUntil, err
}
func (c *Client) DeleteApp(ctx context.Context, slug string) (time.Time, error) {
	var response struct {
		RecoverableUntil time.Time `json:"recoverableUntil"`
	}
	err := c.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/apps/%s", url.PathEscape(slug)), nil, &response)
	return response.RecoverableUntil, err
}
func (c *Client) Restore(ctx context.Context, slug, version string) (model.PublicApp, error) {
	var response struct {
		App model.PublicApp `json:"app"`
	}
	err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/api/v1/apps/%s/versions/%s/restore", url.PathEscape(slug), url.PathEscape(version)), nil, &response)
	return response.App, err
}
func (c *Client) SetPassword(ctx context.Context, slug, password string) (model.PublicApp, error) {
	var response struct {
		App model.PublicApp `json:"app"`
	}
	err := c.doJSON(ctx, http.MethodPut, fmt.Sprintf("/api/v1/apps/%s/password", url.PathEscape(slug)), map[string]string{"password": password}, &response)
	return response.App, err
}

func (c *Client) doJSON(ctx context.Context, method, requestPath string, body, target any) error {
	var input io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+requestPath, input)
	if err != nil {
		return err
	}
	c.authorize(request)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	return json.NewDecoder(response.Body).Decode(target)
}
func (c *Client) authorize(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+c.Token)
	request.Header.Set("Accept", "application/json")
}
func decodeAPIError(response *http.Response) error {
	var value struct{ Code, Message string }
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value)
	if value.Message == "" {
		value.Message = response.Status
	}
	return &APIError{Status: response.StatusCode, Code: value.Code, Message: value.Message}
}

func decodeResponse(response *http.Response, target any) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	return json.NewDecoder(response.Body).Decode(target)
}
