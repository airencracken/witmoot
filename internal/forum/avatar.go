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
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"

	"witmoot/internal/imvault"
)

// Avatars are stored small and square. Bounding the storage this way keeps the
// database modest and lets a 2 MiB source photo become a few kilobytes.
const (
	avatarPixels        = 256
	maxAvatarInputBytes = 2 << 20
	maxAvatarDimension  = 4096
)

// normalizeAvatarImage re-encodes any accepted image as a square PNG at a fixed
// size. Decoding and re-encoding drops any embedded metadata, and the square
// crop keeps faces from being stretched by the layout.
func normalizeAvatarImage(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > maxAvatarInputBytes {
		return nil, errors.New("choose an image no larger than 2 MiB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > maxAvatarDimension || config.Height > maxAvatarDimension {
		return nil, errors.New("choose a valid image up to 4096 by 4096 pixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("choose a PNG, JPEG, or GIF image")
	}
	bounds := img.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	left := bounds.Min.X + (bounds.Dx()-side)/2
	top := bounds.Min.Y + (bounds.Dy()-side)/2
	source := image.Rect(left, top, left+side, top+side)

	scaled := image.NewNRGBA(image.Rect(0, 0, avatarPixels, avatarPixels))
	xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), img, source, xdraw.Over, nil)

	var output bytes.Buffer
	if err := png.Encode(&output, scaled); err != nil {
		return nil, fmt.Errorf("encode PNG: %w", err)
	}
	return output.Bytes(), nil
}

// SaveAvatar normalizes and stores one account's avatar.
func (s *Store) SaveAvatar(ctx context.Context, userID int64, content []byte) error {
	clean, err := normalizeAvatarImage(content)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_avatars(user_id, content, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		userID, clean, time.Now().Unix())
	return err
}

// DeleteAvatar removes an account's avatar, if any.
func (s *Store) DeleteAvatar(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM user_avatars WHERE user_id = ?", userID)
	return err
}

// Avatar returns one account's stored image and when it last changed.
func (s *Store) Avatar(ctx context.Context, userID int64) ([]byte, int64, error) {
	var content []byte
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, "SELECT content, updated_at FROM user_avatars WHERE user_id = ?", userID).Scan(&content, &updatedAt)
	return content, updatedAt, err
}

// HasAvatar reports whether an account has set an avatar.
func (s *Store) HasAvatar(ctx context.Context, userID int64) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_avatars WHERE user_id = ?)", userID).Scan(&exists)
	return exists, err
}

// avatar serves a member's picture. It follows the same audience gate as the
// board, so a Private or Personal instance does not hand avatars to visitors.
func (a *App) avatar(w http.ResponseWriter, r *http.Request) {
	content, updatedAt, err := a.store.Avatar(r.Context(), pathID(r))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("load avatar", "user", r.PathValue("id"), "error", err)
		http.Error(w, "could not load the avatar", http.StatusInternalServerError)
		return
	}
	etag := `"` + strconv.FormatInt(updatedAt, 10) + `"`
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(content)
}

// saveAvatar handles the member's own upload or removal.
func (a *App) saveAvatar(w http.ResponseWriter, r *http.Request) {
	user := *state(r).User
	if r.PostForm.Get("remove") == "1" {
		if err := a.store.DeleteAvatar(r.Context(), user.ID); err != nil {
			a.serverError(w, r, err)
			return
		}
		a.redirect(w, r, "/account?saved=avatar")
		return
	}
	data, err := uploadedBrandImage(r, "avatar")
	if err != nil {
		a.render(w, r, http.StatusUnprocessableEntity, a.accountPage(r, err.Error(), "", user.Email))
		return
	}
	if len(data) == 0 {
		a.render(w, r, http.StatusUnprocessableEntity, a.accountPage(r, "Choose an image to use as your avatar.", "", user.Email))
		return
	}
	if err := a.store.SaveAvatar(r.Context(), user.ID, data); err != nil {
		a.render(w, r, http.StatusUnprocessableEntity, a.accountPage(r, err.Error(), "", user.Email))
		return
	}
	a.redirect(w, r, "/account?saved=avatar")
}

// avatarLibrary lists the member's imvault images to import as an avatar.
func (a *App) avatarLibrary(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		a.fail(w, r, 503, "Image integration is not configured. Ask the owner to connect an imvault server.")
		return
	}
	token, err := a.imageToken(r.Context(), state(r).User.ID)
	if err != nil {
		a.redirect(w, r, "/account/imvault")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if len(query) > 100 || offset < 0 || offset > 100000 || offset%12 != 0 {
		a.fail(w, r, 400, "That library search is not valid.")
		return
	}
	files, more, err := a.vault.List(r.Context(), token, query, offset)
	p := Page{View: "avatar-library", Title: "Choose an avatar", Library: files, LibraryMore: more, LibraryLoaded: true, LibraryOffset: offset, Query: query, ImageServer: a.vault.Base}
	if err != nil {
		p.ImageError = imvault.ErrUnavailable.Error()
	}
	a.render(w, r, 200, p)
}

// importAvatar copies one chosen imvault image into the local avatar store. The
// copy is deliberate: the avatar keeps working if imvault is unreachable later,
// and the original stays where it belongs.
func (a *App) importAvatar(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		a.fail(w, r, 503, "Image integration is not configured.")
		return
	}
	id := strings.TrimSpace(r.PostForm.Get("image_id"))
	if !imvault.IDPattern.MatchString(id) {
		a.fail(w, r, 400, "Choose an image from your imvault library.")
		return
	}
	token, err := a.imageToken(r.Context(), state(r).User.ID)
	if err != nil {
		a.redirect(w, r, "/account/imvault")
		return
	}
	file, err := a.vault.File(r.Context(), token, id)
	if err != nil {
		a.fail(w, r, 422, "That image is not in your imvault library.")
		return
	}
	data, _, err := a.vault.Image(r.Context(), token, file.ID, file.Rendition())
	if err != nil {
		a.fail(w, r, 502, "That image could not be fetched from imvault right now.")
		return
	}
	if err := a.store.SaveAvatar(r.Context(), state(r).User.ID, data); err != nil {
		a.fail(w, r, 422, "That image could not be used as an avatar.")
		return
	}
	a.redirect(w, r, "/account?saved=avatar")
}

// removeMemberAvatar lets an owner clear an avatar for moderation.
func (a *App) removeMemberAvatar(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteAvatar(r.Context(), pathID(r)); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/members?saved=avatar-removed")
}
