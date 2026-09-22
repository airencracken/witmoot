package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"witmoot/internal/imvault"
)

const maxImages = 4

func (a *App) imageAccount(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		a.fail(w, r, 503, "Image integration is not configured. Ask the owner to connect an imvault server.")
		return
	}
	a.render(w, r, 200, Page{View: "image-account", Title: "Your imvault connection", Saved: r.URL.Query().Get("saved") == "1"})
}

func (a *App) connectImages(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		a.fail(w, r, 503, "Image integration is not configured.")
		return
	}
	token := strings.TrimSpace(r.PostForm.Get("api_key"))
	valid := len(token) > 0 && len(token) <= 512
	for _, c := range token {
		if c < 33 || c > 126 {
			valid = false
		}
	}
	if !valid {
		a.render(w, r, 422, Page{View: "image-account", Title: "Your imvault connection", Error: "Enter a valid imvault API key."})
		return
	}
	name, err := a.vault.Me(r.Context(), token)
	if err != nil {
		a.render(w, r, 422, Page{View: "image-account", Title: "Your imvault connection", Error: "That key could not connect to imvault. Check the key and try again."})
		return
	}
	encrypted, err := a.encryptImageToken(state(r).User.ID, token)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.ConnectImages(r.Context(), state(r).User.ID, Connection{Username: name, Server: a.vault.Base, Token: encrypted}); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/account/imvault?saved=1")
}

func (a *App) disconnectImages(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DisconnectImages(r.Context(), state(r).User.ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.redirect(w, r, "/account/imvault?saved=1")
}

func (a *App) decorateImages(r *http.Request, p *Page) {
	p.ImagesEnabled = a.vault != nil
	p.ImageLinks = r.PostForm.Get("image_links")
	p.SelectedImages = make(map[string]bool)
	for _, id := range r.PostForm["image_ids"] {
		p.SelectedImages[id] = true
	}
	if a.vault == nil {
		return
	}
	p.ImageServer = a.vault.Base
	if state(r).User == nil {
		return
	}
	connection, err := a.store.Connection(r.Context(), state(r).User.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		p.ImageError = "Your imvault connection could not be loaded."
		return
	}
	if connection.Server != a.vault.Base {
		return
	}
	p.ImageConnected, p.ImageUsername = true, connection.Username
	if p.View != "compose" && p.View != "topic" {
		return
	}
	if !p.CanPost {
		return
	}
	p.LibraryPicking = true
	token, err := a.imageToken(r.Context(), state(r).User.ID)
	if err != nil {
		p.ImageError = "Reconnect your imvault account to use your images."
		return
	}
	p.Library, p.LibraryMore, err = a.vault.List(r.Context(), token, "", 0)
	if err != nil {
		p.ImageError = imvault.ErrUnavailable.Error()
	}
	carryImageSelection(p, r.PostForm["image_ids"])
}

func carryImageSelection(p *Page, ids []string) {
	p.SelectedImages = make(map[string]bool)
	for _, id := range ids {
		if !imvault.IDPattern.MatchString(id) || p.SelectedImages[id] || len(p.SelectedImages) >= maxImages {
			continue
		}
		p.SelectedImages[id] = true
		visible := false
		for _, f := range p.Library {
			if f.ID == id {
				visible = true
				break
			}
		}
		if !visible {
			p.CarriedImages = append(p.CarriedImages, id)
		}
	}
}

func (a *App) library(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		a.fail(w, r, 503, "Image integration is not configured.")
		return
	}
	token, err := a.imageToken(r.Context(), state(r).User.ID)
	if err != nil {
		a.redirect(w, r, "/account/imvault")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if len(query) > 400 || offset < 0 || offset > 100000 || offset%12 != 0 {
		a.fail(w, r, 400, "That library search is not valid.")
		return
	}
	files, more, err := a.vault.List(r.Context(), token, query, offset)
	p := Page{View: "library", Title: "Your imvault library", Library: files, LibraryMore: more, LibraryOffset: offset, Query: query, ImageServer: a.vault.Base}
	if err != nil {
		p.ImageError = imvault.ErrUnavailable.Error()
	}
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "image-library" {
		p.LibraryPicking = true
		carryImageSelection(&p, r.URL.Query()["image_ids"])
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := a.templates.ExecuteTemplate(w, "image-library", p); err != nil {
			slog.Error("render image library", "error", err)
		}
		return
	}
	a.render(w, r, 200, p)
}

func (a *App) libraryImage(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		http.NotFound(w, r)
		return
	}
	token, err := a.imageToken(r.Context(), state(r).User.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// The metadata API verifies ownership before a credentialed media request.
	f, err := a.vault.File(r.Context(), token, r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a.serveImage(w, r, token, f.ID, "thumb")
}

func (a *App) image(w http.ResponseWriter, r *http.Request) {
	if a.vault == nil {
		http.NotFound(w, r)
		return
	}
	img, err := a.store.Attachment(r.Context(), pathID(r), state(r).User)
	if err != nil || img.Server != a.vault.Base {
		http.NotFound(w, r)
		return
	}
	token := ""
	if img.CredentialUserID.Valid {
		token, err = a.imageToken(r.Context(), img.CredentialUserID.Int64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// Reconnecting with another imvault account must not turn old image IDs into grants.
		if _, err := a.vault.File(r.Context(), token, img.RemoteID); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	a.serveImage(w, r, token, img.RemoteID, img.Rendition)
}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request, token, id, rendition string) {
	data, contentType, err := a.vault.Image(r.Context(), token, id, rendition)
	if err != nil {
		http.Error(w, "This image is currently unavailable.", 502)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "imvault-image"}))
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
}

// prepareImages returns only references verified for this member. Its cleanup
// removes newly uploaded files if the post fails, never selected or linked files.
func (a *App) prepareImages(r *http.Request) ([]Attachment, func(), error) {
	noop := func() {}
	var uploads = r.MultipartForm
	count := len(r.PostForm["image_ids"]) + len(strings.Fields(r.PostForm.Get("image_links")))
	if uploads != nil {
		count += len(uploads.File["images"])
	}
	if count == 0 {
		return nil, noop, nil
	}
	if a.vault == nil {
		return nil, noop, errors.New("image integration is not configured")
	}
	if count > maxImages {
		return nil, noop, errors.New("attach up to four images per message")
	}
	token, tokenErr := a.imageToken(r.Context(), state(r).User.ID)
	if tokenErr != nil {
		token = ""
	}
	var images []Attachment
	var created []string
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, id := range created {
			if err := a.vault.Delete(ctx, token, id); err != nil {
				slog.Error("could not remove unattached imvault upload", "image_id", id)
			}
		}
	}
	seen := make(map[string]bool)
	add := func(f imvault.File, authenticated bool) {
		if seen[f.ID] {
			return
		}
		seen[f.ID] = true
		img := Attachment{Server: a.vault.Base, RemoteID: f.ID, Name: f.Name, Rendition: f.Rendition()}
		if authenticated {
			img.CredentialUserID = sql.NullInt64{Int64: state(r).User.ID, Valid: true}
		}
		images = append(images, img)
	}
	for _, id := range r.PostForm["image_ids"] {
		if token == "" {
			return nil, cleanup, errors.New("connect your imvault account to choose images")
		}
		f, err := a.vault.File(r.Context(), token, id)
		if err != nil {
			return nil, cleanup, errors.New("one of the chosen images is no longer available in your imvault library")
		}
		add(f, true)
	}
	for _, link := range strings.Fields(r.PostForm.Get("image_links")) {
		id, err := a.vault.ParseLink(link)
		if err != nil {
			return nil, cleanup, err
		}
		if seen[id] {
			continue
		}
		if _, _, err := a.vault.Image(r.Context(), "", id, "thumb"); err == nil {
			add(imvault.File{ID: id, Name: "Image from imvault", Kind: "animated"}, false)
			continue
		}
		if token == "" {
			return nil, cleanup, errors.New("that image is not public; connect the imvault account that owns it")
		}
		f, err := a.vault.File(r.Context(), token, id)
		if err != nil {
			return nil, cleanup, errors.New("that image is not public or in your connected imvault library")
		}
		add(f, true)
	}
	if uploads != nil {
		for _, header := range uploads.File["images"] {
			if token == "" {
				return nil, cleanup, errors.New("connect your imvault account to upload images")
			}
			if header.Size > imvault.MaxImageBytes {
				return nil, cleanup, errors.New("each image must be 8 MiB or smaller")
			}
			file, err := header.Open()
			if err != nil {
				return nil, cleanup, errors.New("that image could not be read")
			}
			data, err := io.ReadAll(io.LimitReader(file, imvault.MaxImageBytes+1))
			file.Close()
			if err != nil {
				return nil, cleanup, errors.New("that image could not be read")
			}
			f, err := a.vault.Upload(r.Context(), token, header.Filename, data)
			if err != nil {
				return nil, cleanup, fmt.Errorf("image upload failed: %w", err)
			}
			created = append(created, f.ID)
			add(f, true)
		}
	}
	return images, cleanup, nil
}
