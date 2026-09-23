package forum

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"witmoot/internal/imvault"
)

//go:embed templates/*.html static/*
var assets embed.FS

const pageSize = 20

type Config struct {
	Name           string
	BaseURL        string
	SecureCookies  bool
	ImvaultURL     string
	ImageKey       []byte
	TrustedProxies []netip.Prefix
}

type App struct {
	store     *Store
	config    Config
	templates *template.Template
	handler   http.Handler
	limiter   limiter
	dummyHash string
	vault     *imvault.Client
}

type requestState struct {
	User *User
	CSRF string
	Mode Mode
}
type stateKey struct{}

type Page struct {
	Invitations                                             []Invitation
	InviteCode, InviteLabel, InviteUses, InviteDays         string
	ImagesEnabled, ImageConnected, LibraryMore              bool
	ImageUsername, ImageServer, ImageLinks, ImageError      string
	Library                                                 []imvault.File
	LibraryOffset                                           int
	SelectedImages                                          map[string]bool
	CarriedImages                                           []string
	LibraryPicking                                          bool
	LibraryLoaded                                           bool
	Name, Title, View, CSRF, Error                          string
	User                                                    *User
	Mode                                                    Mode
	CanRead, CanPost, Saved                                 bool
	Boards                                                  []Board
	Board                                                   Board
	BoardMembers                                            []BoardMember
	BoardAction                                             string
	BoardContents                                           BoardContents
	Deleted                                                 bool
	Post                                                    Post
	ComposeAudience                                         Audience
	Topics                                                  []Topic
	Topic                                                   Topic
	Posts                                                   []Post
	Stats                                                   Stats
	Query, Prev, Next                                       string
	Username, TitleInput, BodyInput, Invitation, InviteLink string
}

func New(store *Store, config Config) (*App, error) {
	if strings.TrimSpace(config.Name) == "" {
		config.Name = "Witmoot"
	}
	baseURL, err := canonicalBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	config.BaseURL = baseURL
	tmpl, err := template.New("forum").Funcs(template.FuncMap{
		"add":     func(a, b int) int { return a + b },
		"date":    func(unix int64) string { return time.Unix(unix, 0).UTC().Format("Jan 2, 2006") },
		"stamp":   func(unix int64) string { return time.Unix(unix, 0).UTC().Format("Jan 2, 2006 · 15:04 UTC") },
		"iso":     func(unix int64) string { return time.Unix(unix, 0).UTC().Format(time.RFC3339) },
		"initial": func(name string) string { r, _ := utf8.DecodeRuneInString(name); return strings.ToUpper(string(r)) },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	dummy, err := HashPassword(randomToken())
	if err != nil {
		return nil, err
	}
	a := &App{store: store, config: config, templates: tmpl, dummyHash: dummy, limiter: limiter{entries: make(map[string]rateEntry)}}
	if config.ImvaultURL != "" {
		if len(config.ImageKey) != 32 {
			return nil, errors.New("imvault integration requires a 32-byte encryption key")
		}
		a.vault, err = imvault.New(config.ImvaultURL)
		if err != nil {
			return nil, err
		}
	}
	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /login", a.loginForm)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("GET /join", a.joinForm)
	mux.HandleFunc("POST /join", a.join)
	mux.HandleFunc("POST /logout", a.signedIn(a.logout))
	mux.HandleFunc("GET /boards/{id}", a.readable(a.board))
	mux.HandleFunc("GET /boards/{id}/new", a.private(a.newTopic))
	mux.HandleFunc("POST /boards/{id}/new", a.private(a.createTopic))
	mux.HandleFunc("GET /topics/{id}", a.readable(a.topic))
	mux.HandleFunc("POST /topics/{id}/replies", a.private(a.reply))
	mux.HandleFunc("GET /posts/{id}/edit", a.private(a.editPostForm))
	mux.HandleFunc("POST /posts/{id}/edit", a.private(a.editPost))
	mux.HandleFunc("GET /boards/manage", a.owner(a.manageBoards))
	mux.HandleFunc("GET /boards/new", a.owner(a.boardSettings))
	mux.HandleFunc("POST /boards/new", a.owner(a.saveBoardSettings))
	mux.HandleFunc("GET /boards/{id}/settings", a.owner(a.boardSettings))
	mux.HandleFunc("POST /boards/{id}/settings", a.owner(a.saveBoardSettings))
	for _, action := range []string{"archive", "restore", "delete"} {
		mux.HandleFunc("GET /boards/{id}/"+action, a.owner(a.boardActionForm(action)))
		mux.HandleFunc("POST /boards/{id}/"+action, a.owner(a.boardAction(action)))
	}
	mux.HandleFunc("GET /archive", a.readable(a.archive))
	mux.HandleFunc("GET /recent", a.readable(a.recent))
	mux.HandleFunc("GET /search", a.readable(a.search))
	mux.HandleFunc("GET /invites", a.private(a.invites))
	mux.HandleFunc("POST /invites", a.private(a.createInvite))
	mux.HandleFunc("POST /invites/{id}/revoke", a.private(a.revokeInvite))
	mux.HandleFunc("GET /settings", a.signedIn(a.settings))
	mux.HandleFunc("POST /settings", a.signedIn(a.saveSettings))
	mux.HandleFunc("GET /account/imvault", a.signedIn(a.imageAccount))
	mux.HandleFunc("POST /account/imvault", a.private(a.connectImages))
	mux.HandleFunc("POST /account/imvault/disconnect", a.signedIn(a.disconnectImages))
	mux.HandleFunc("GET /imvault/library", a.private(a.library))
	mux.HandleFunc("GET /imvault/library/{id}/image", a.private(a.libraryImage))
	mux.HandleFunc("GET /images/{id}", a.readable(a.image))
	a.handler = http.NewCrossOriginProtection().Handler(a.middleware(mux))
	return a, nil
}

func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

func (a *App) cookieName(name string) string {
	if a.config.SecureCookies {
		return "__Host-witmoot_" + name
	}
	return "witmoot_" + name
}

func (a *App) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(name), Value: value, Path: "/", HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: age})
}

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		mode, err := a.store.Mode(r.Context())
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		state := requestState{Mode: mode}
		if c, err := r.Cookie(a.cookieName("csrf")); err == nil && validToken(c.Value) {
			state.CSRF = c.Value
		}
		if state.CSRF == "" {
			state.CSRF = randomToken()
			a.cookie(w, "csrf", state.CSRF, 86400)
		}
		if c, err := r.Cookie(a.cookieName("session")); err == nil && validToken(c.Value) {
			user, err := a.store.Session(r.Context(), tokenHash(c.Value))
			if err != nil {
				a.serverError(w, r, err)
				return
			}
			state.User = user
		}
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, state))
		if r.Method == http.MethodPost {
			// URL-encoded Unicode can use twelve bytes per character on the wire.
			limit := int64(256 << 10)
			contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			multipart := contentType == "multipart/form-data"
			if multipart && state.canPost() && a.vault != nil {
				limit = maxImages*imvault.MaxImageBytes + (1 << 20)
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			var formErr error
			if multipart {
				formErr = r.ParseMultipartForm(8 << 20)
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
			} else {
				formErr = r.ParseForm()
			}
			if formErr != nil {
				a.fail(w, r, http.StatusBadRequest, "That form could not be read. Please try again.")
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(state.CSRF)) != 1 {
				a.fail(w, r, http.StatusForbidden, "This form has expired. Reload the page and try again.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func validToken(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func state(r *http.Request) requestState {
	v, _ := r.Context().Value(stateKey{}).(requestState)
	return v
}

func (a *App) signedIn(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if state(r).User == nil {
			a.redirect(w, r, "/login")
			return
		}
		next(w, r)
	}
}

func (a *App) private(next http.HandlerFunc) http.HandlerFunc { return a.signedIn(a.readable(next)) }

func (a *App) redirect(w http.ResponseWriter, r *http.Request, path string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, p Page) {
	p.Name, p.CSRF, p.User = a.config.Name, state(r).CSRF, state(r).User
	p.Mode, p.CanRead, p.CanPost = state(r).Mode, state(r).canRead(), state(r).canPost()
	if p.Board.ID != 0 {
		p.CanPost = p.CanPost && boardWriteError(p.Board.Access, p.Board.Archived) == nil
	}
	if p.Topic.ID != 0 {
		p.CanPost = p.CanPost && boardWriteError(p.Topic.BoardAccess, p.Topic.BoardArchived) == nil
	}
	p.ComposeAudience = p.Board.Audience(p.Mode)
	for i := range p.Posts {
		p.Posts[i].CanEdit = p.CanPost && p.Posts[i].AuthorID == p.User.ID
	}
	a.decorateImages(r, &p)
	var buffer bytes.Buffer
	if err := a.templates.ExecuteTemplate(&buffer, "layout", p); err != nil {
		slog.Error("render page", "error", err)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buffer.WriteTo(w); err != nil {
		slog.Debug("response interrupted", "error", err)
	}
}

func (a *App) fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	a.render(w, r, status, Page{View: "error", Title: http.StatusText(status), Error: message})
}
func (a *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "path", r.URL.Path, "error", err)
	a.fail(w, r, 500, "Something went wrong. Please try again in a moment.")
}
func (a *App) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errReadOnly) || errors.Is(err, errArchived) || errors.Is(err, errPersonal) || errors.Is(err, errOwner) {
		a.fail(w, r, 403, err.Error())
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, 404, "We could not find that conversation.")
		return
	}
	a.serverError(w, r, err)
}

func pathID(r *http.Request) int64 { id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64); return id }

func pageNumber(r *http.Request) (int, error) {
	value := r.URL.Query().Get("page")
	if value == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > 100000 {
		return 0, errors.New("invalid page")
	}
	return page, nil
}

func pagination(r *http.Request, p *Page, page int, more bool) {
	link := func(n int) string {
		query := r.URL.Query()
		query.Set("page", strconv.Itoa(n))
		return r.URL.Path + "?" + query.Encode()
	}
	if page > 1 {
		p.Prev = link(page - 1)
	}
	if more {
		p.Next = link(page + 1)
	}
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	if !state(r).canRead() {
		if state(r).User != nil {
			a.fail(w, r, 403, errPersonal.Error())
			return
		}
		a.render(w, r, 200, Page{View: "welcome", Title: "A place for your people"})
		return
	}
	boards, err := a.store.Boards(r.Context(), state(r).User)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	stats, err := a.store.Stats(r.Context(), state(r).User)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	boards = slices.DeleteFunc(boards, func(b Board) bool { return b.Archived })
	a.render(w, r, 200, Page{View: "home", Title: "Our board", Boards: boards, Stats: stats})
}

func (a *App) board(w http.ResponseWriter, r *http.Request) {
	b, err := a.store.Board(r.Context(), pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	a.topicList(w, r, Page{View: "board", Title: b.Name, Board: b}, b.ID, "")
}

func (a *App) topicList(w http.ResponseWriter, r *http.Request, p Page, boardID int64, query string) {
	page, err := pageNumber(r)
	if err != nil {
		a.fail(w, r, 400, "That page number is not valid.")
		return
	}
	topics, more, err := a.store.Topics(r.Context(), boardID, query, pageSize, (page-1)*pageSize, state(r).User)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p.Topics = topics
	pagination(r, &p, page, more)
	a.render(w, r, 200, p)
}

func (a *App) recent(w http.ResponseWriter, r *http.Request) {
	a.topicList(w, r, Page{View: "recent", Title: "Recent conversations"}, 0, "")
}

func (a *App) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 100 {
		a.fail(w, r, 400, "Keep your search to 100 characters or fewer.")
		return
	}
	p := Page{View: "search", Title: "Find a conversation", Query: query}
	if query == "" {
		a.render(w, r, 200, p)
		return
	}
	a.topicList(w, r, p, 0, query)
}

func (a *App) topic(w http.ResponseWriter, r *http.Request) { a.showTopic(w, r, 200, "", "") }

func (a *App) showTopic(w http.ResponseWriter, r *http.Request, status int, message, body string) {
	topic, err := a.store.Topic(r.Context(), pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	page, err := pageNumber(r)
	if err != nil {
		a.fail(w, r, 400, "That page number is not valid.")
		return
	}
	posts, more, err := a.store.Posts(r.Context(), topic.ID, pageSize, (page-1)*pageSize, state(r).User)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.PostImages(r.Context(), posts); err != nil {
		a.serverError(w, r, err)
		return
	}
	p := Page{View: "topic", Title: topic.Title, Topic: topic, Posts: posts, Error: message, BodyInput: body}
	pagination(r, &p, page, more)
	a.render(w, r, status, p)
}

func (a *App) newTopic(w http.ResponseWriter, r *http.Request) {
	b, err := a.store.Board(r.Context(), pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	if err := boardWriteError(b.Access, b.Archived); err != nil {
		a.storeError(w, r, err)
		return
	}
	a.render(w, r, 200, Page{View: "compose", Title: "Start a conversation", Board: b})
}

func validText(value string, min, max int) bool {
	n := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && n >= min && n <= max
}

func (a *App) createTopic(w http.ResponseWriter, r *http.Request) {
	b, err := a.store.Board(r.Context(), pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	if err := boardWriteError(b.Access, b.Archived); err != nil {
		a.storeError(w, r, err)
		return
	}
	title, body := strings.TrimSpace(r.PostForm.Get("title")), strings.TrimSpace(r.PostForm.Get("body"))
	if r.PostForm.Get("load_images") == "1" {
		a.render(w, r, 200, Page{View: "compose", Title: "Start a conversation", Board: b, TitleInput: title, BodyInput: body})
		return
	}
	message := ""
	if !validText(title, 3, 160) {
		message = "Give your conversation a title between 3 and 160 characters."
	} else if !validText(body, 1, 20000) {
		message = "Write a message between 1 and 20,000 characters."
	}
	if message != "" {
		a.render(w, r, 422, Page{View: "compose", Title: "Start a conversation", Board: b, Error: message, TitleInput: title, BodyInput: body})
		return
	}
	audience := Audience(r.PostForm.Get("audience"))
	if audience == "" {
		audience = AudienceMembers
	} // Forms from before this feature remain members-only.
	if audience != b.Audience(state(r).Mode) {
		a.render(w, r, 422, Page{View: "compose", Title: "Start a conversation", Board: b, Error: errAudienceChanged.Error(), TitleInput: title, BodyInput: body})
		return
	}
	images, cleanup, err := a.prepareImages(r)
	if err != nil {
		cleanup()
		a.render(w, r, 422, Page{View: "compose", Title: "Start a conversation", Board: b, Error: err.Error(), TitleInput: title, BodyInput: body})
		return
	}
	id, err := a.store.CreateTopic(r.Context(), b.ID, state(r).User.ID, title, body, audience, images...)
	if err != nil {
		cleanup()
		if errors.Is(err, errAudienceChanged) || errors.Is(err, errPersonal) {
			a.render(w, r, 422, Page{View: "compose", Title: "Start a conversation", Board: b, Error: err.Error(), TitleInput: title, BodyInput: body})
			return
		}
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, fmt.Sprintf("/topics/%d", id))
}

func (a *App) reply(w http.ResponseWriter, r *http.Request) {
	topic, err := a.store.Topic(r.Context(), pathID(r), state(r).User)
	if err != nil {
		a.storeError(w, r, err)
		return
	}
	if err := boardWriteError(topic.BoardAccess, topic.BoardArchived); err != nil {
		a.storeError(w, r, err)
		return
	}
	body := strings.TrimSpace(r.PostForm.Get("body"))
	if r.PostForm.Get("load_images") == "1" {
		a.showTopic(w, r, 200, "", body)
		return
	}
	if !validText(body, 1, 20000) {
		a.showTopic(w, r, 422, "Write a reply between 1 and 20,000 characters.", body)
		return
	}
	images, cleanup, err := a.prepareImages(r)
	if err != nil {
		cleanup()
		a.showTopic(w, r, 422, err.Error(), body)
		return
	}
	id, count, err := a.store.Reply(r.Context(), topic.ID, state(r).User.ID, body, images...)
	if err != nil {
		cleanup()
		if errors.Is(err, errPersonal) {
			a.fail(w, r, 403, err.Error())
			return
		}
		a.storeError(w, r, err)
		return
	}
	a.redirect(w, r, fmt.Sprintf("/topics/%d?page=%d#post-%d", topic.ID, (count-1)/pageSize+1, id))
}
