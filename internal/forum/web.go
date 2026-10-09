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
	"io"
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

	"github.com/airencracken/comfylib/smtp"
	"github.com/airencracken/comfylib/token"

	"witmoot/internal/imvault"
)

//go:embed templates/*.html static/*
var assets embed.FS

const pageSize = 20

type Config struct {
	// Version identifies the running build, supplied by the executable.
	Version        string
	Name           string
	SourceURL      string
	BaseURL        string
	SecureCookies  bool
	ImvaultURL     string
	ImageKey       []byte
	TrustedProxies []netip.Prefix
	// Mail describes an optional SMTP relay. An empty Host leaves mail
	// disabled, and password reset links are handed over by an owner instead.
	Mail smtp.Config
}

type App struct {
	store       *Store
	config      Config
	templates   *template.Template
	handler     http.Handler
	limiter     *limiter
	dummyHash   string
	vault       *imvault.Client
	mailer      smtp.Sender
	exportSlots chan struct{}
}

type requestState struct {
	User *User
	CSRF string
	Mode Mode
}
type stateKey struct{}

type Page struct {
	SharedSource, AlbumTitle                                    string
	AlbumCount                                                  int
	Version, FooterVersion                                      string
	ShowVersion                                                 bool
	Invitations                                                 []Invitation
	InviteMembers                                               []InviteMember
	InviteCode, InviteLabel, InviteUses, InviteDays             string
	ImagesEnabled, ImageConnected, LibraryMore                  bool
	ImageUsername, ImageServer, ImageLinks, ImageError          string
	Library                                                     []imvault.File
	LibraryOffset                                               int
	SelectedImages                                              map[string]bool
	CarriedImages                                               []string
	LibraryPicking                                              bool
	LibraryLoaded                                               bool
	Name, Title, View, CSRF, Error                              string
	SourceURL, WelcomeTitle, WelcomeText, MascotURL, FaviconURL string
	CustomMascot, BrandingDraft                                 bool
	User                                                        *User
	Mode                                                        Mode
	CanRead, CanPost, Saved                                     bool
	CanManageInvites                                            bool
	Boards                                                      []Board
	Board                                                       Board
	BoardMembers                                                []BoardMember
	BoardGroups                                                 []BoardGroup
	Groups                                                      []Group
	Group                                                       Group
	GroupMembers                                                []GroupMember
	GroupBoards                                                 []GroupBoard
	BoardAction                                                 string
	BoardContents                                               BoardContents
	Deleted                                                     bool
	Post                                                        Post
	ComposeAudience                                             Audience
	Topics                                                      []Topic
	Topic                                                       Topic
	Posts                                                       []Post
	Stats                                                       Stats
	Query, Prev, Next                                           string
	Username, TitleInput, BodyInput, Invitation, InviteLink     string
	Email, Notice                                               string
	MailEnabled                                                 bool
	ResetToken, ResetUser                                       string
	ResetLink, ResetFor                                         string
	ResetEmailed                                                bool
	Members                                                     []MemberReset
	HasAvatar                                                   bool
	AnimateAvatars                                              bool
	HouseRules, OwnerContact, CommunityAction                   string
	Owners                                                      []string
	Member                                                      User
	Events                                                      []CommunityEvent
	TimezoneInput                                               string
	TimezoneSuggestions                                         []string
	timezone                                                    *time.Location
}

func New(store *Store, config Config) (*App, error) {
	if config.Version == "" {
		config.Version = "devel"
	}
	if strings.TrimSpace(config.Name) == "" {
		config.Name = "Witmoot"
	}
	if err := CheckSiteName(config.Name); err != nil {
		return nil, err
	}
	if config.SourceURL == "" {
		config.SourceURL = "https://github.com/airencracken/witmoot"
	}
	baseURL, err := CanonicalBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	config.BaseURL = baseURL
	if strings.HasPrefix(baseURL, "https://") {
		config.SecureCookies = true
	}
	tmpl, err := template.New("forum").Funcs(template.FuncMap{
		"add":         func(a, b int) int { return a + b },
		"access":      accessLabel,
		"iso":         func(unix int64) string { return time.Unix(unix, 0).UTC().Format(time.RFC3339) },
		"initial":     func(name string) string { r, _ := utf8.DecodeRuneInString(name); return strings.ToUpper(string(r)) },
		"audience":    func(a string) string { return Audience(a).Label() },
		"segments":    segments,
		"resetExpiry": func() string { return HumanDuration(ResetLinkTTL) },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	unguessable, _ := token.New()
	dummy, err := HashPassword(unguessable)
	if err != nil {
		return nil, err
	}
	var mailer smtp.Sender = smtp.Disabled{}
	if config.Mail.Host != "" {
		// An unknown TLS mode or a malformed sender stops startup rather
		// than sending in plain text or failing on the first reset link.
		if mailer, err = smtp.New(config.Mail); err != nil {
			return nil, err
		}
	}
	a := &App{store: store, config: config, templates: tmpl, dummyHash: dummy, limiter: newLimiter(), mailer: mailer, exportSlots: make(chan struct{}, 1)}
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
	mux.HandleFunc("GET /branding/{name}", a.brandingAsset)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if _, err := io.WriteString(w, "ok\n"); err != nil {
			slog.Debug("response interrupted", "error", err)
		}
	})
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /about", a.about)
	mux.HandleFunc("GET /moderation", a.owner(a.moderation))
	mux.HandleFunc("GET /login", a.loginForm)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("GET /join", a.joinForm)
	mux.HandleFunc("POST /join", a.join)
	mux.HandleFunc("POST /logout", a.signedIn(a.logout))
	mux.HandleFunc("GET /share", a.private(a.discussionHandoff))
	mux.HandleFunc("GET /posts/{id}/album-preview", a.readable(a.albumPreview))
	mux.HandleFunc("GET /boards/{id}", a.readable(a.board))
	mux.HandleFunc("GET /boards/{id}/new", a.private(a.newTopic))
	mux.HandleFunc("POST /boards/{id}/new", a.private(a.createTopic))
	mux.HandleFunc("GET /topics/{id}", a.readable(a.topic))
	mux.HandleFunc("POST /topics/{id}/replies", a.private(a.reply))
	mux.HandleFunc("GET /posts/{id}/edit", a.private(a.editPostForm))
	mux.HandleFunc("POST /posts/{id}/edit", a.private(a.editPost))
	mux.HandleFunc("GET /posts/{id}/remove", a.owner(a.removePostForm))
	mux.HandleFunc("POST /posts/{id}/remove", a.owner(a.removePost))
	mux.HandleFunc("GET /boards/manage", a.owner(a.manageBoards))
	mux.HandleFunc("GET /groups", a.owner(a.manageGroups))
	mux.HandleFunc("GET /groups/new", a.owner(a.groupSettings))
	mux.HandleFunc("POST /groups/new", a.owner(a.saveGroup))
	mux.HandleFunc("GET /groups/{id}", a.owner(a.groupSettings))
	mux.HandleFunc("POST /groups/{id}", a.owner(a.saveGroup))
	mux.HandleFunc("GET /groups/{id}/delete", a.owner(a.groupDeleteForm))
	mux.HandleFunc("POST /groups/{id}/delete", a.owner(a.deleteGroup))
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
	mux.HandleFunc("POST /invites/members/{id}/permission", a.owner(a.setInvitePermission))
	mux.HandleFunc("GET /community/export", a.owner(a.handleCommunityExport))
	mux.HandleFunc("GET /settings", a.owner(a.settings))
	mux.HandleFunc("POST /settings", a.owner(a.saveSettings))
	mux.HandleFunc("POST /settings/branding-assets", a.owner(a.saveBrandingAssets))
	mux.HandleFunc("GET /account", a.signedIn(a.account))
	mux.HandleFunc("GET /account/delete", a.signedIn(a.accountDeletion))
	mux.HandleFunc("POST /account/delete", a.signedIn(a.deleteAccount))
	mux.HandleFunc("GET /account/export", a.signedIn(a.handleAccountExport))
	mux.HandleFunc("POST /account/password", a.signedIn(a.changePassword))
	mux.HandleFunc("POST /account/email", a.signedIn(a.saveEmail))
	mux.HandleFunc("POST /account/timezone", a.signedIn(a.saveTimezone))
	mux.HandleFunc("POST /account/avatar", a.signedIn(a.saveAvatar))
	mux.HandleFunc("POST /account/avatar-preference", a.signedIn(a.saveAvatarPreference))
	mux.HandleFunc("GET /account/avatar/imvault", a.signedIn(a.avatarLibrary))
	mux.HandleFunc("POST /account/avatar/imvault", a.signedIn(a.importAvatar))
	mux.HandleFunc("GET /avatars/{id}", a.readable(a.avatar))
	mux.HandleFunc("GET /reset/{token}", a.resetForm)
	mux.HandleFunc("POST /reset/{token}", a.resetPassword)
	mux.HandleFunc("GET /members", a.owner(a.members))
	for _, action := range []string{"suspend", "restore"} {
		mux.HandleFunc("GET /members/{id}/"+action, a.owner(a.memberActionForm(action)))
		mux.HandleFunc("POST /members/{id}/"+action, a.owner(a.memberAction(action)))
	}
	mux.HandleFunc("POST /members/{id}/reset", a.owner(a.createResetLink))
	mux.HandleFunc("POST /members/{id}/reset/revoke", a.owner(a.revokeResetLink))
	mux.HandleFunc("POST /members/{id}/avatar/remove", a.owner(a.removeMemberAvatar))
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

// Request bodies get a deadline that suits the route. Image uploads may carry
// tens of megabytes over a slow connection, matching the five minutes the
// example proxies allow; every other form is small and must arrive quickly.
var (
	formTimeout   = 30 * time.Second
	uploadTimeout = 5 * time.Minute
)

// setDeadlines replaces the server-wide deadlines, which leave request reads
// to the handler. Writers without deadline support, such as test recorders,
// are left alone.
func setDeadlines(w http.ResponseWriter, r *http.Request) {
	read, write := formTimeout, 2*time.Minute
	if contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); r.Method == http.MethodPost && contentType == "multipart/form-data" {
		read, write = uploadTimeout, uploadTimeout+time.Minute
	}
	now := time.Now()
	controller := http.NewResponseController(w)
	for _, err := range []error{controller.SetReadDeadline(now.Add(read)), controller.SetWriteDeadline(now.Add(write))} {
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			slog.Debug("set request deadline", "path", loggedPath(r), "error", err)
		}
	}
}

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setDeadlines(w, r)
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
		session := ""
		if c, err := r.Cookie(a.cookieName("session")); err == nil && validToken(c.Value) {
			user, err := a.store.Session(r.Context(), token.Hash(c.Value))
			if err != nil {
				a.serverError(w, r, err)
				return
			}
			if state.User = user; user != nil {
				session = c.Value
			}
		}
		state.CSRF = a.expectedCSRF(w, r, session)
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, state))
		if isMutating(r.Method) {
			// URL-encoded Unicode can use twelve bytes per character on the wire.
			limit := int64(256 << 10)
			contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			multipart := contentType == "multipart/form-data"
			if multipart && (r.URL.Path == "/settings/branding-assets" || r.URL.Path == "/account/avatar") {
				limit = 5 << 20
			}
			if multipart && state.canPost() && a.vault != nil {
				limit = maxImages*imvault.MaxImageBytes + (1 << 20)
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			provided, err := providedCSRF(r, multipart)
			if err != nil {
				a.fail(w, r, http.StatusBadRequest, "That form could not be read. Please try again.")
				return
			}
			if subtle.ConstantTimeCompare([]byte(provided), []byte(state.CSRF)) != 1 {
				a.fail(w, r, http.StatusForbidden, "This form has expired. Reload the page and try again.")
				return
			}
			// Only a request that carries its token gets its upload read.
			if multipart {
				formErr := r.ParseMultipartForm(8 << 20)
				if r.MultipartForm != nil {
					defer func() {
						if err := r.MultipartForm.RemoveAll(); err != nil {
							slog.Error("remove temporary upload files", "error", err)
						}
					}()
				}
				if formErr != nil {
					a.fail(w, r, http.StatusBadRequest, "That form could not be read. Please try again.")
					return
				}
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
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
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
	p.Version = a.config.Version
	brand, err := a.store.LoadBranding(r.Context(), SiteBranding{
		Name: a.config.Name, SourceURL: a.config.SourceURL,
		WelcomeTitle: "Good company. Conversations worth keeping.",
		WelcomeText:  "For the plans, the little updates, and the stories that deserve more than a passing message.",
	})
	if err == nil {
		if brand.ShowVersion {
			p.FooterVersion = a.config.Version
		}
		if !p.BrandingDraft {
			p.ShowVersion = brand.ShowVersion
			p.Name, p.SourceURL, p.WelcomeTitle, p.WelcomeText = brand.Name, brand.SourceURL, brand.WelcomeTitle, brand.WelcomeText
			p.HouseRules, p.OwnerContact = brand.HouseRules, brand.OwnerContact
		}
		mascot, favicon, assetErr := a.store.BrandingAssetState(r.Context())
		if assetErr == nil {
			p.MascotURL = "/static/moot-knight.png"
			if mascot {
				p.MascotURL, p.CustomMascot = "/branding/mascot", true
			}
			if favicon {
				p.FaviconURL = "/branding/favicon"
			}
		}
	} else {
		slog.Error("load site branding", "error", err)
		p.Name, p.SourceURL = a.config.Name, a.config.SourceURL
		p.MascotURL = "/static/moot-knight.png"
	}
	if p.Name == "" {
		p.Name = a.config.Name
	}
	p.CSRF, p.User = state(r).CSRF, state(r).User
	p.timezone = time.UTC
	if p.User != nil {
		p.timezone = displayTimezone(p.User.Timezone)
	}
	p.Mode, p.CanRead, p.CanPost = state(r).Mode, state(r).canRead(), state(r).canPost()
	p.CanManageInvites = state(r).User != nil && state(r).User.Role == "owner"
	if p.Board.ID != 0 {
		p.CanPost = p.CanPost && boardWriteError(p.Board.Access, p.Board.Archived) == nil
	}
	if p.Topic.ID != 0 {
		p.CanPost = p.CanPost && boardWriteError(p.Topic.BoardAccess, p.Topic.BoardArchived) == nil
	}
	p.ComposeAudience = p.Board.Audience(p.Mode)
	for i := range p.Posts {
		p.Posts[i].CanEdit = p.CanPost && !p.Posts[i].Removed && p.Posts[i].AuthorID == p.User.ID
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
	slog.Error("request failed", "path", loggedPath(r), "error", err)
	a.fail(w, r, 500, "Something went wrong. Please try again in a moment.")
}

// userError is a failure whose text is written as interface copy, a complete
// sentence for the person who filled in the form, rather than a Go error
// string meant to be wrapped.
type userError string

func (e userError) Error() string { return string(e) }

// loggedPath is the request path with any reset token removed: a reset link in
// a log is as good as the password it replaces.
func loggedPath(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/reset/") {
		return "/reset/[token]"
	}
	return r.URL.Path
}

func (a *App) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errReadOnly) || errors.Is(err, errArchived) || errors.Is(err, errPersonal) || errors.Is(err, errOwner) || communityError(err) {
		a.fail(w, r, 403, err.Error())
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		a.fail(w, r, 404, notFoundMessage(r))
		return
	}
	a.serverError(w, r, err)
}

// notFoundMessage names what the matched route looks up, so a missing record
// is described in the terms of the page that asked for it.
func notFoundMessage(r *http.Request) string {
	_, path, _ := strings.Cut(r.Pattern, " ")
	for _, route := range []struct{ prefix, thing string }{
		{"/boards/", "board"}, {"/topics/", "conversation"}, {"/posts/", "message"},
		{"/invites/members/", "member"}, {"/invites/", "invitation"},
		{"/members/", "member"}, {"/groups/", "group"},
	} {
		if strings.HasPrefix(path, route.prefix) {
			return "We could not find that " + route.thing + "."
		}
	}
	return "We could not find what you were looking for."
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
	a.postReferences(posts)
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
