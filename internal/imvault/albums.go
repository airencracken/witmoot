// SPDX-License-Identifier: AGPL-3.0-or-later
package imvault

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"
)

type AlbumPreview struct {
	Title, Slug, Visibility string
	ImageCount              int `json:"image_count"`
}

func (c *Client) ParseAlbumLink(raw string) (string, error) {
	u, err := url.Parse(raw)
	base, _ := url.Parse(c.Base)
	if err != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return "", errors.New("use an album link from the configured imvault server")
	}
	prefix := base.Path + "/a/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", errors.New("use an imvault album page")
	}
	slug := strings.TrimPrefix(u.Path, prefix)
	if !IDPattern.MatchString(slug) {
		return "", errors.New("invalid album link")
	}
	return slug, nil
}
func (c *Client) AlbumPreview(ctx context.Context, token, slug string) (AlbumPreview, error) {
	var a AlbumPreview
	if !IDPattern.MatchString(slug) || token == "" {
		return a, ErrUnavailable
	}
	if err := c.json(ctx, "/api/v1/albums/"+slug+"/preview", token, &a); err != nil {
		return AlbumPreview{}, err
	}
	if a.Slug != slug || a.Visibility != "public" || a.ImageCount < 0 || a.Title == "" || !utf8.ValidString(a.Title) || utf8.RuneCountInString(a.Title) > 120 {
		return AlbumPreview{}, ErrUnavailable
	}
	return a, nil
}
