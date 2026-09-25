package forum

import (
	"archive/zip"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// exportFormat and exportVersion identify the archive so an importer, or a
// person reading it in five years, knows what they are holding.
const (
	exportFormat  = "witmoot-account-export"
	exportVersion = 1
)

// The export is built from purpose-written types rather than the store structs.
// Serialising a User would put the password hash in a file the account can
// download, which is exactly the mistake worth designing against.

type exportManifest struct {
	Format     string        `json:"format"`
	Version    int           `json:"version"`
	ExportedAt string        `json:"exported_at"`
	Site       exportSite    `json:"site"`
	Account    exportAccount `json:"account"`
	Boards     []exportBoard `json:"boards"`
	Topics     []exportTopic `json:"topics"`
	Posts      []exportPost  `json:"posts"`
	Note       string        `json:"note"`
}

type exportSite struct {
	Name      string `json:"name"`
	SourceURL string `json:"source_url,omitempty"`
}

type exportAccount struct {
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	Email     string `json:"email,omitempty"`
	HasAvatar bool   `json:"has_avatar"`
}

type exportBoard struct {
	ID       int64  `json:"id"`
	Category string `json:"category"`
	Name     string `json:"name"`
}

type exportTopic struct {
	ID        int64  `json:"id"`
	BoardID   int64  `json:"board_id"`
	Title     string `json:"title"`
	Audience  string `json:"audience"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Posts     int    `json:"post_count"`
	URL       string `json:"url"`
}

type exportPost struct {
	ID          int64              `json:"id"`
	TopicID     int64              `json:"topic_id"`
	BoardID     int64              `json:"board_id"`
	TopicTitle  string             `json:"topic_title"`
	BoardName   string             `json:"board_name"`
	Number      int64              `json:"number"`
	Body        string             `json:"body"`
	CreatedAt   string             `json:"created_at"`
	EditedAt    string             `json:"edited_at,omitempty"`
	Revision    int64              `json:"revision"`
	URL         string             `json:"url"`
	Attachments []exportAttachment `json:"attachments,omitempty"`
}

type exportAttachment struct {
	Name      string `json:"name"`
	Rendition string `json:"rendition"`
	Server    string `json:"server"`
	RemoteID  string `json:"remote_id"`
}

// exportView is what the offline archive page renders.
type exportView struct {
	Manifest exportManifest
}

const exportNote = "Your own messages only; other people's replies are not included. " +
	"Shared images are listed by reference to the imvault preview that was posted. " +
	"Fetch your originals from your imvault account export. " +
	"Passwords, sessions, and image connections are never included."

// handleAccountExport streams a member's own contributions, plus a manifest
// describing them, as a zip. It is written straight to the response: there is
// no reason to keep a second copy of somebody's words on the server.
func (a *App) handleAccountExport(w http.ResponseWriter, r *http.Request) {
	user := state(r).User
	ctx := r.Context()

	contributions, err := a.store.MemberContributions(ctx, user.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	avatar, _, avatarErr := a.store.Avatar(ctx, user.ID)
	if avatarErr != nil && !errors.Is(avatarErr, sql.ErrNoRows) {
		a.serverError(w, r, avatarErr)
		return
	}

	origin := a.origin(r)
	manifest := exportManifest{
		Format:     exportFormat,
		Version:    exportVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Site:       exportSite{Name: a.siteName(r), SourceURL: a.config.SourceURL},
		Account: exportAccount{
			Username:  user.Username,
			Role:      user.Role,
			CreatedAt: time.Unix(user.CreatedAt, 0).UTC().Format(time.RFC3339),
			Email:     user.Email,
			HasAvatar: len(avatar) > 0,
		},
		Note: exportNote,
	}
	for _, b := range contributions.Boards {
		manifest.Boards = append(manifest.Boards, exportBoard{ID: b.ID, Category: b.Category, Name: b.Name})
	}
	for _, t := range contributions.Topics {
		manifest.Topics = append(manifest.Topics, exportTopic{
			ID: t.ID, BoardID: t.BoardID, Title: t.Title, Audience: t.Audience,
			CreatedAt: time.Unix(t.CreatedAt, 0).UTC().Format(time.RFC3339),
			UpdatedAt: time.Unix(t.UpdatedAt, 0).UTC().Format(time.RFC3339),
			Posts:     t.Posts,
			URL:       fmt.Sprintf("%s/topics/%d", origin, t.ID),
		})
	}
	for _, p := range contributions.Posts {
		post := exportPost{
			ID: p.ID, TopicID: p.TopicID, BoardID: p.BoardID,
			TopicTitle: p.TopicTitle, BoardName: p.BoardName, Number: p.Number,
			Body:      p.Body,
			CreatedAt: time.Unix(p.CreatedAt, 0).UTC().Format(time.RFC3339),
			Revision:  p.Revision,
			URL:       fmt.Sprintf("%s/topics/%d?page=%d#post-%d", origin, p.TopicID, (p.Number-1)/int64(pageSize)+1, p.ID),
		}
		if p.EditedAt > 0 {
			post.EditedAt = time.Unix(p.EditedAt, 0).UTC().Format(time.RFC3339)
		}
		for _, attachment := range p.Attachments {
			post.Attachments = append(post.Attachments, exportAttachment{
				Name: attachment.Name, Rendition: attachment.Rendition,
				Server: attachment.Server, RemoteID: attachment.RemoteID,
			})
		}
		manifest.Posts = append(manifest.Posts, post)
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exportFilename(user.Username)))
	w.Header().Set("Cache-Control", "no-store")

	archive := zip.NewWriter(w)
	defer archive.Close()

	if err := writeJSONEntry(archive, "manifest.json", manifest); err != nil {
		slog.Error("export: write manifest", "user", user.ID, "error", err)
		return
	}
	if err := a.writeArchivePage(archive, manifest); err != nil {
		slog.Error("export: write archive page", "user", user.ID, "error", err)
		return
	}
	if len(avatar) > 0 {
		entry, err := archive.Create("avatar.png")
		if err != nil {
			slog.Error("export: write avatar", "user", user.ID, "error", err)
			return
		}
		if _, err := entry.Write(avatar); err != nil {
			slog.Error("export: write avatar", "user", user.ID, "error", err)
			return
		}
	}
	slog.Info("account exported", "user", user.ID, "posts", len(manifest.Posts))
}

// humanStamp renders an RFC3339 timestamp for the offline archive.
func humanStamp(value string) string {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Format("Jan 2, 2006 · 15:04 UTC")
	}
	return value
}

// exportFilename is the name the browser saves the archive as.
func exportFilename(username string) string {
	return fmt.Sprintf("witmoot-%s-%s.zip", username, time.Now().UTC().Format("2006-01-02"))
}

// writeArchivePage adds the readable, offline view of the same material.
func (a *App) writeArchivePage(archive *zip.Writer, manifest exportManifest) error {
	entry, err := archive.Create("archive.html")
	if err != nil {
		return err
	}
	return a.templates.ExecuteTemplate(entry, "export", exportView{Manifest: manifest})
}

// writeJSONEntry adds a JSON document to the archive.
func writeJSONEntry(archive *zip.Writer, name string, payload any) error {
	entry, err := archive.Create(name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(entry)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}
