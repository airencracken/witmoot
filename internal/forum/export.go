package forum

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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
	Format     string           `json:"format"`
	Version    int              `json:"version"`
	ExportedAt string           `json:"exported_at"`
	Site       exportSite       `json:"site"`
	Account    exportAccount    `json:"account"`
	Boards     []exportBoard    `json:"boards"`
	Topics     []exportTopic    `json:"topics"`
	Posts      []exportPost     `json:"posts"`
	Note       string           `json:"note"`
	Members    []exportMember   `json:"members,omitempty"`
	Policy     *CommunityPolicy `json:"policy,omitempty"`
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
	Author      string             `json:"author"`
	AuthorID    int64              `json:"author_id"`
	CreatedAt   string             `json:"created_at"`
	EditedAt    string             `json:"edited_at,omitempty"`
	Revision    int64              `json:"revision"`
	URL         string             `json:"url"`
	Attachments []exportAttachment `json:"attachments,omitempty"`
	Removed     bool               `json:"removed,omitempty"`
}

type exportAttachment struct {
	Name      string `json:"name"`
	Rendition string `json:"rendition"`
	Server    string `json:"server"`
	RemoteID  string `json:"remote_id"`
	URL       string `json:"url"`
	Path      string `json:"path,omitempty"`
	Note      string `json:"note,omitempty"`
	ID        int64  `json:"-"`
}

// exportView is what the offline archive page renders.
type exportView struct {
	Manifest exportManifest
}

const exportNote = "Your own messages only; other people's replies are not included. " +
	"Available shared previews are included, with links to their Imvault pages. " +
	"Fetch your originals from your imvault account export. " +
	"Removed messages appear as placeholders, without their former text or image references. " +
	"Boards and conversations you can no longer read are listed without their names. " +
	"Passwords, sessions, and image connections are never included."

// Exports are prepared in a private temporary file before successful headers.
// Only one archive is prepared or downloaded at a time.
func (a *App) handleAccountExport(w http.ResponseWriter, r *http.Request) {
	a.handleExport(w, r, false)
}

func (a *App) handleCommunityExport(w http.ResponseWriter, r *http.Request) {
	a.handleExport(w, r, true)
}

func (a *App) handleExport(w http.ResponseWriter, r *http.Request, community bool) {
	select {
	case a.exportSlots <- struct{}{}:
		defer func() { <-a.exportSlots }()
	default:
		w.Header().Set("Retry-After", "10")
		http.Error(w, "Another export is being prepared. Try again shortly.", 503)
		return
	}
	user := state(r).User
	ctx := r.Context()

	var contributions Contributions
	var err error
	if community {
		contributions, err = a.store.CommunityContributions(ctx, user)
	} else {
		contributions, err = a.store.MemberContributions(ctx, user)
	}
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
	if community {
		manifest.Format = "witmoot-community-export"
		manifest.Policy = contributions.Policy
		manifest.Note = "Every board and conversation, including private ones. Available shared previews are included; unavailable previews retain Imvault links. Passwords, sessions, account email addresses and image credentials are omitted."
		manifest.Account.Email = ""
		manifest.Members = contributions.Members
	}
	for _, b := range contributions.Boards {
		manifest.Boards = append(manifest.Boards, exportBoard(b))
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
			Author:    p.Author,
			AuthorID:  p.AuthorID,
			CreatedAt: time.Unix(p.CreatedAt, 0).UTC().Format(time.RFC3339),
			Revision:  p.Revision,
			Removed:   p.Removed,
			URL:       fmt.Sprintf("%s/topics/%d?page=%d#post-%d", origin, p.TopicID, (p.Number-1)/int64(pageSize)+1, p.ID),
		}
		if p.EditedAt > 0 {
			post.EditedAt = time.Unix(p.EditedAt, 0).UTC().Format(time.RFC3339)
		}
		for _, attachment := range p.Attachments {
			post.Attachments = append(post.Attachments, exportAttachment{Name: attachment.Name, Rendition: attachment.Rendition, Server: attachment.Server, RemoteID: attachment.RemoteID, ID: attachment.ID, URL: attachment.Server + "/f/" + attachment.RemoteID})
		}
		manifest.Posts = append(manifest.Posts, post)
	}

	temp, err := os.CreateTemp("", "witmoot-export-*.zip")
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := a.writeExport(zip.NewWriter(temp), manifest, avatar, exportOptions{ctx, user}); err != nil {
		a.serverError(w, r, err)
		return
	}
	info, err := temp.Stat()
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	filename := exportFilename(user.Username)
	if community {
		filename = exportFilename("community")
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if r.Method == http.MethodHead {
		return
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		a.serverError(w, r, err)
		return
	}
	if n, err := io.Copy(w, temp); err != nil || n != info.Size() {
		panic(http.ErrAbortHandler)
	}
	slog.Info("data exported", "user", user.ID, "community", community, "posts", len(manifest.Posts))
}

type exportMember struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
type exportOptions struct {
	ctx  context.Context
	user *User
}

// writeExport writes every archive entry and then the zip directory. Close is
// part of the archive, so its error is as fatal as any other.
func (a *App) writeExport(archive *zip.Writer, manifest exportManifest, avatar []byte, options ...exportOptions) error {
	if len(options) > 0 {
		if err := a.exportImages(archive, &manifest, options[0]); err != nil {
			return err
		}
	}
	if err := writeJSONEntry(archive, "manifest.json", manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := a.writeArchivePage(archive, manifest); err != nil {
		return fmt.Errorf("write archive page: %w", err)
	}
	if len(avatar) > 0 {
		entry, err := archive.Create("avatar.png")
		if err != nil {
			return fmt.Errorf("write avatar: %w", err)
		}
		if _, err := entry.Write(avatar); err != nil {
			return fmt.Errorf("write avatar: %w", err)
		}
	}
	return archive.Close()
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
