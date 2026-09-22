// Package imvault implements the subset of imvault's v1 API used by Witmoot.
package imvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxImageBytes = 8 << 20

var IDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var renditionVersionPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
var ErrUnavailable = errors.New("imvault could not complete that request; check your connection and try again")

type Client struct {
	Base string
	HTTP *http.Client
}
type File struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Mime       string `json:"mime"`
	Kind       string `json:"kind"`
	Visibility string `json:"visibility"`
}

func (f File) IsImage() bool {
	return IDPattern.MatchString(f.ID) && (f.Kind == "image" || f.Kind == "animated")
}
func (f File) Rendition() string {
	if f.Kind == "image" {
		return "preview"
	}
	return "thumb"
}

func New(base string) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return nil, errors.New("WITMOOT_IMVAULT_URL must be an HTTP(S) server URL without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{Base: u.String(), HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Client) request(ctx context.Context, method, path, token, contentType string, body io.Reader) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return nil, ErrUnavailable
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Accept", "application/json")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	return resp, nil
}

func (c *Client) json(ctx context.Context, path, token string, target any) error {
	resp, err := c.request(ctx, "GET", path, token, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, target) != nil {
		return ErrUnavailable
	}
	return nil
}

func (c *Client) Me(ctx context.Context, token string) (string, error) {
	var account struct {
		Username string `json:"username"`
	}
	if err := c.json(ctx, "/api/v1/me", token, &account); err != nil {
		return "", err
	}
	if account.Username == "" {
		return "", ErrUnavailable
	}
	return account.Username, nil
}

func (c *Client) List(ctx context.Context, token, query string, offset int) ([]File, bool, error) {
	var response struct {
		Files []File `json:"files"`
		Total int    `json:"total"`
	}
	path := fmt.Sprintf("/api/v1/files?limit=12&offset=%d&q=%s", offset, url.QueryEscape(query))
	if err := c.json(ctx, path, token, &response); err != nil {
		return nil, false, err
	}
	var files []File
	for _, f := range response.Files {
		if f.IsImage() {
			files = append(files, f)
		}
	}
	return files, offset+len(response.Files) < response.Total, nil
}

func (c *Client) File(ctx context.Context, token, id string) (File, error) {
	var f File
	if !IDPattern.MatchString(id) {
		return f, errors.New("invalid imvault image ID")
	}
	if err := c.json(ctx, "/api/v1/files/"+id, token, &f); err != nil {
		return f, err
	}
	if f.ID != id || !f.IsImage() {
		return f, errors.New("choose an image from imvault")
	}
	return f, nil
}

func (c *Client) ParseLink(link string) (string, error) {
	u, err := url.Parse(link)
	base, _ := url.Parse(c.Base)
	if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("use an image link from the configured imvault server")
	}
	prefix := base.Path + "/f/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", errors.New("use an imvault image page, raw, preview, or thumbnail link")
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, prefix), "/")
	if len(parts) > 2 || !IDPattern.MatchString(parts[0]) || len(parts) == 2 && parts[1] != "raw" && parts[1] != "preview" && parts[1] != "thumb" {
		return "", errors.New("invalid imvault image link")
	}
	if u.RawQuery != "" {
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(parts) != 2 || parts[1] == "raw" || len(query) != 1 || len(query["v"]) != 1 || !renditionVersionPattern.MatchString(query.Get("v")) {
			return "", errors.New("invalid imvault image version")
		}
	}
	// The version only controls browser caching. Resolve the ID against imvault
	// and fetch its current rendition; never forward supplied query parameters.
	return parts[0], nil
}

func ImageMIME(data []byte) string {
	switch mime := http.DetectContentType(data); mime {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
		return mime
	}
	return ""
}

func (c *Client) Image(ctx context.Context, token, id, rendition string) ([]byte, string, error) {
	if !IDPattern.MatchString(id) || rendition != "preview" && rendition != "thumb" {
		return nil, "", ErrUnavailable
	}
	resp, err := c.request(ctx, "GET", "/f/"+id+"/"+rendition, token, "", nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil || len(data) > MaxImageBytes || ImageMIME(data) == "" {
		return nil, "", ErrUnavailable
	}
	return data, ImageMIME(data), nil
}

func (c *Client) Upload(ctx context.Context, token, name string, data []byte) (File, error) {
	if len(data) > MaxImageBytes || ImageMIME(data) == "" {
		return File{}, errors.New("choose JPEG, PNG, WebP, or GIF images up to 8 MiB each")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("visibility", "private"); err != nil {
		return File{}, err
	}
	if err := writer.WriteField("metadata", "hidden"); err != nil {
		return File{}, err
	}
	part, err := writer.CreateFormFile("files", name)
	if err != nil {
		return File{}, err
	}
	if _, err := part.Write(data); err != nil {
		return File{}, err
	}
	if err := writer.Close(); err != nil {
		return File{}, err
	}
	resp, err := c.request(ctx, "POST", "/api/v1/upload", token, writer.FormDataContentType(), &body)
	if err != nil {
		return File{}, err
	}
	defer resp.Body.Close()
	var response struct {
		Files  []File   `json:"files"`
		Errors []string `json:"errors"`
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(payload) > 1<<20 || json.Unmarshal(payload, &response) != nil {
		return File{}, ErrUnavailable
	}
	if resp.StatusCode != 201 || len(response.Files) != 1 || len(response.Errors) != 0 || !response.Files[0].IsImage() || response.Files[0].Visibility != "private" {
		// Only clean up IDs returned from this upload, never existing library files.
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, f := range response.Files {
			if IDPattern.MatchString(f.ID) {
				if err := c.Delete(cleanup, token, f.ID); err != nil {
					slog.Error("could not remove rejected imvault upload", "image_id", f.ID)
				}
			}
		}
		return File{}, ErrUnavailable
	}
	return response.Files[0], nil
}

func (c *Client) Delete(ctx context.Context, token, id string) error {
	if !IDPattern.MatchString(id) {
		return ErrUnavailable
	}
	resp, err := c.request(ctx, "DELETE", "/api/v1/files/"+id, token, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ErrUnavailable
	}
	return nil
}
