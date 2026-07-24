package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
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
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	go func() {
		defer pipeWriter.Close()
		metadataPart, partErr := writer.CreateFormField("metadata")
		if partErr == nil {
			partErr = json.NewEncoder(metadataPart).Encode(metadata)
		}
		if partErr == nil && len(icon) > 0 {
			var iconPart io.Writer
			iconPart, partErr = writer.CreateFormFile("icon", "icon")
			if partErr == nil {
				_, partErr = iconPart.Write(icon)
			}
		}
		if partErr == nil {
			var packagePart io.Writer
			packagePart, partErr = writer.CreateFormFile("package", "package"+path.Ext(packagePath))
			if partErr == nil {
				_, partErr = io.Copy(packagePart, &progressReader{reader: file, total: metadata.FileSize, output: progress})
			}
		}
		if closeErr := writer.Close(); partErr == nil {
			partErr = closeErr
		}
		if partErr != nil {
			_ = pipeWriter.CloseWithError(partErr)
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/v1/packages", pipeReader)
	if err != nil {
		return model.UploadResult{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	c.authorize(request)
	response, err := c.HTTP.Do(request)
	if err != nil {
		return model.UploadResult{}, err
	}
	defer response.Body.Close()
	var result model.UploadResult
	if err := decodeResponse(response, &result); err != nil {
		return model.UploadResult{}, err
	}
	return result, nil
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

type progressReader struct {
	reader      io.Reader
	total, read int64
	output      io.Writer
	last        int
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	r.read += int64(n)
	if r.output != nil && r.total > 0 {
		percent := int(r.read * 100 / r.total)
		if percent != r.last {
			_, _ = fmt.Fprintf(r.output, "\r上传 %d%%", percent)
			r.last = percent
			if percent == 100 {
				_, _ = fmt.Fprintln(r.output)
			}
		}
	}
	return n, err
}

func decodeResponse(response *http.Response, target any) error {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeAPIError(response)
	}
	return json.NewDecoder(response.Body).Decode(target)
}
