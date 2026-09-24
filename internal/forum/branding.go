package forum

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxBrandImageBytes = 2 << 20

type SiteBranding struct {
	Name         string
	SourceURL    string
	WelcomeTitle string
	WelcomeText  string
}

func (s *Store) LoadBranding(ctx context.Context, defaults SiteBranding) (SiteBranding, error) {
	branding := defaults
	err := s.db.QueryRowContext(ctx, `SELECT name, source_url, welcome_title, welcome_text
		FROM instance_branding WHERE id = 1`).Scan(&branding.Name, &branding.SourceURL, &branding.WelcomeTitle, &branding.WelcomeText)
	if errors.Is(err, sql.ErrNoRows) {
		return defaults, nil
	}
	if err != nil {
		return SiteBranding{}, fmt.Errorf("load site branding: %w", err)
	}
	if strings.TrimSpace(branding.Name) == "" {
		branding.Name = defaults.Name
	}
	if strings.TrimSpace(branding.WelcomeTitle) == "" {
		branding.WelcomeTitle = defaults.WelcomeTitle
	}
	if strings.TrimSpace(branding.WelcomeText) == "" {
		branding.WelcomeText = defaults.WelcomeText
	}
	return branding, nil
}

func (s *Store) SaveBranding(ctx context.Context, branding SiteBranding) error {
	branding, err := cleanBranding(branding)
	if err != nil {
		return err
	}
	if err := saveBranding(ctx, s.db, branding); err != nil {
		return fmt.Errorf("save site branding: %w", err)
	}
	return nil
}

type brandingWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func cleanBranding(branding SiteBranding) (SiteBranding, error) {
	branding.Name = strings.TrimSpace(branding.Name)
	branding.SourceURL = strings.TrimSpace(branding.SourceURL)
	branding.WelcomeTitle = strings.TrimSpace(branding.WelcomeTitle)
	branding.WelcomeText = strings.TrimSpace(branding.WelcomeText)
	if !brandTextValid(branding.Name, 80, false) || !brandTextValid(branding.WelcomeTitle, 120, false) || !brandTextValid(branding.WelcomeText, 2000, true) {
		return SiteBranding{}, errors.New("keep the site name under 80 characters, welcome title under 120, and welcome text under 2000")
	}
	if len(branding.SourceURL) > 512 || !validSourceURL(branding.SourceURL) {
		return SiteBranding{}, errors.New("source link must be blank or an HTTP(S) URL")
	}
	return branding, nil
}

func saveBranding(ctx context.Context, writer brandingWriter, branding SiteBranding) error {
	_, err := writer.ExecContext(ctx, `INSERT INTO instance_branding(id, name, source_url, welcome_title, welcome_text)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, source_url=excluded.source_url,
		welcome_title=excluded.welcome_title, welcome_text=excluded.welcome_text`,
		branding.Name, branding.SourceURL, branding.WelcomeTitle, branding.WelcomeText)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) SaveBrandingAssets(ctx context.Context, mascot, favicon []byte, removeMascot, removeFavicon bool) error {
	if len(mascot) > 0 {
		clean, err := normalizeBrandImage(mascot)
		if err != nil {
			return fmt.Errorf("mascot image: %w", err)
		}
		mascot = clean
	}
	if len(favicon) > 0 {
		clean, err := normalizeBrandImage(favicon)
		if err != nil {
			return fmt.Errorf("favicon image: %w", err)
		}
		favicon = clean
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, asset := range []struct {
		name   string
		data   []byte
		remove bool
	}{{"mascot", mascot, removeMascot}, {"favicon", favicon, removeFavicon}} {
		if asset.remove {
			if _, err := tx.ExecContext(ctx, `DELETE FROM branding_assets WHERE name = ?`, asset.name); err != nil {
				return err
			}
		} else if len(asset.data) > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO branding_assets(name, content) VALUES (?, ?)
				ON CONFLICT(name) DO UPDATE SET content=excluded.content`, asset.name, asset.data); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) BrandingAsset(ctx context.Context, name string) ([]byte, error) {
	if name != "mascot" && name != "favicon" {
		return nil, sql.ErrNoRows
	}
	var content []byte
	if err := s.db.QueryRowContext(ctx, `SELECT content FROM branding_assets WHERE name = ?`, name).Scan(&content); err != nil {
		return nil, err
	}
	return content, nil
}

func (s *Store) BrandingAssetState(ctx context.Context) (mascot, favicon bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM branding_assets WHERE name = 'mascot'),
		EXISTS(SELECT 1 FROM branding_assets WHERE name = 'favicon')`).Scan(&mascot, &favicon)
	return
}

func normalizeBrandImage(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > maxBrandImageBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 || int64(config.Width)*int64(config.Height) > 4_194_304 {
		return nil, errors.New("choose a valid image up to 2048 by 2048 pixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("choose a PNG, JPEG, or GIF image")
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return output.Bytes(), nil
}

func validSourceURL(value string) bool {
	if value == "" {
		return true
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

func brandTextValid(value string, max int, multiline bool) bool {
	if utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}

func (a *App) brandingAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "mascot" && name != "favicon" {
		http.NotFound(w, r)
		return
	}
	content, err := a.store.BrandingAsset(r.Context(), name)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("load branding asset", "name", name, "error", err)
		http.Error(w, "could not load image", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(content)
}

func (a *App) saveBrandingAssets(w http.ResponseWriter, r *http.Request) {
	mascot, err := uploadedBrandImage(r, "mascot")
	if err != nil {
		a.render(w, r, http.StatusUnprocessableEntity, a.settingsPage(r, err.Error()))
		return
	}
	favicon, err := uploadedBrandImage(r, "favicon")
	if err != nil {
		a.render(w, r, http.StatusUnprocessableEntity, a.settingsPage(r, err.Error()))
		return
	}
	removeMascot := r.PostForm.Get("remove_mascot") == "1"
	removeFavicon := r.PostForm.Get("remove_favicon") == "1"
	if len(mascot) == 0 && len(favicon) == 0 && !removeMascot && !removeFavicon {
		a.render(w, r, http.StatusUnprocessableEntity, a.settingsPage(r, "Choose an image or select one to remove."))
		return
	}
	if err := a.store.SaveBrandingAssets(r.Context(), mascot, favicon, removeMascot, removeFavicon); err != nil {
		a.render(w, r, http.StatusUnprocessableEntity, a.settingsPage(r, err.Error()))
		return
	}
	a.redirect(w, r, "/settings?saved=1")
}

func uploadedBrandImage(r *http.Request, name string) ([]byte, error) {
	file, header, err := r.FormFile(name)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if header.Size > maxBrandImageBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBrandImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBrandImageBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	return data, nil
}
