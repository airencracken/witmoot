// Package admin is a small Bubble Tea interface over the same account
// operations the CLI exposes. It holds no privileged logic of its own: every
// action goes through forum.Store, so the web UI, the commands, and this view
// cannot drift apart.
package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"witmoot/internal/forum"
	"witmoot/internal/mail"
)

const resetTTL = 24 * time.Hour

// Options are the small pieces of instance context the view needs.
type Options struct {
	BaseURL  string
	SiteName string
	Mailer   mail.Sender
}

type screen int

const (
	screenList screen = iota
	screenActions
	screenInput
	screenConfirm
	screenResult
)

type action int

const (
	actionReset action = iota
	actionResetEmail
	actionCancelReset
	actionSetPassword
	actionBack
)

type item struct {
	label string
	kind  action
}

// Model is the Bubble Tea model. It is a value so tests can drive Update
// directly without a terminal.
type Model struct {
	store   *forum.Store
	opts    Options
	members []forum.MemberReset
	table   table.Model

	screen  screen
	target  forum.MemberReset
	items   []item
	cursor  int
	inputs  []textinput.Model
	focus   int
	title   string
	help    string
	submit  func(*Model) error
	confirm string
	onYes   func(*Model) error

	link    string
	emailed bool
	flash   string
	err     error

	width, height int
	quitting      bool
}

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
)

// New builds the model and loads the member list.
func New(store *forum.Store, opts Options) (Model, error) {
	if opts.SiteName == "" {
		opts.SiteName = "Witmoot"
	}
	m := Model{store: store, opts: opts}
	m.table = table.New(
		table.WithColumns([]table.Column{
			{Title: "ID", Width: 5},
			{Title: "User", Width: 18},
			{Title: "Role", Width: 8},
			{Title: "Email", Width: 26},
			{Title: "Reset", Width: 14},
		}),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	if err := m.reload(); err != nil {
		return m, err
	}
	return m, nil
}

// Run starts the interactive view. It needs a terminal.
func Run(store *forum.Store, opts Options) error {
	m, err := New(store, opts)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

func (m *Model) reload() error {
	members, err := m.store.Members(context.Background())
	if err != nil {
		return err
	}
	m.members = members
	rows := make([]table.Row, 0, len(members))
	for _, member := range members {
		reset := ""
		if member.Pending {
			reset = "link waiting"
		}
		email := member.Email
		if email == "" {
			email = "—"
		}
		rows = append(rows, table.Row{fmt.Sprint(member.ID), member.Username, member.Role, email, reset})
	}
	m.table.SetRows(rows)
	if cursor := m.table.Cursor(); cursor >= len(rows) && len(rows) > 0 {
		m.table.SetCursor(len(rows) - 1)
	}
	return nil
}

func (m Model) mailer() mail.Sender {
	if m.opts.Mailer == nil {
		return mail.Disabled{}
	}
	return m.opts.Mailer
}

func (m Model) resetURL(token string) string {
	if m.opts.BaseURL != "" {
		return m.opts.BaseURL + "/reset/" + token
	}
	return "/reset/" + token
}

func (m Model) View() string {
	switch m.screen {
	case screenActions:
		return m.actionsView()
	case screenInput:
		return m.inputView()
	case screenConfirm:
		return m.confirmView()
	case screenResult:
		return m.resultView()
	default:
		return m.listView()
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		m.table.SetWidth(atLeast(size.Width-4, 20))
		m.table.SetHeight(atLeast(size.Height-9, 3))
	}
	switch m.screen {
	case screenActions:
		return m.updateActions(msg)
	case screenInput:
		return m.updateInput(msg)
	case screenConfirm:
		return m.updateConfirm(msg)
	case screenResult:
		return m.updateResult(msg)
	default:
		return m.updateList(msg)
	}
}

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q":
			m.quitting = true
			return m, tea.Quit
		case "n":
			return m.startCreateOwner()
		case "r":
			m.err = m.reload()
			return m, nil
		case "enter":
			if len(m.members) == 0 {
				return m, nil
			}
			m.target = m.members[m.table.Cursor()]
			m.items = m.memberActions()
			m.cursor = 0
			m.screen = screenActions
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m Model) memberActions() []item {
	items := []item{{"Create a reset link", actionReset}}
	if m.mailer().Enabled() && m.target.Email != "" {
		items = append(items, item{"Email a reset link", actionResetEmail})
	}
	if m.target.Pending {
		items = append(items, item{"Cancel the reset link", actionCancelReset})
	}
	items = append(items,
		item{"Set a new password", actionSetPassword},
		item{"Back", actionBack},
	)
	return items
}

func (m Model) updateActions(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "q", "esc":
		m.screen = screenList
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "enter":
		switch m.items[m.cursor].kind {
		case actionReset:
			return m.issueReset(false)
		case actionResetEmail:
			return m.issueReset(true)
		case actionCancelReset:
			m.confirm = fmt.Sprintf("Cancel the reset link for %s? The link will stop working.", m.target.Username)
			m.onYes = (*Model).cancelReset
			m.screen = screenConfirm
		case actionSetPassword:
			return m.startSetPassword()
		case actionBack:
			m.screen = screenList
		}
	}
	return m, nil
}

func (m Model) issueReset(email bool) (tea.Model, tea.Cmd) {
	token := forum.NewToken()
	if err := m.store.CreateAuthToken(context.Background(), m.target.ID, forum.TokenPasswordReset, forum.TokenHash(token), time.Now().Add(resetTTL)); err != nil {
		m.err = err
		return m, nil
	}
	m.err = nil
	m.link = m.resetURL(token)
	m.emailed = false
	if email {
		message := mail.Message{
			To:      m.target.Email,
			Subject: "Choose a new password for " + m.opts.SiteName,
			Body:    resetBody(m.target.Username, m.opts.SiteName, m.link),
		}
		if err := m.mailer().Send(context.Background(), message); err != nil {
			m.err = err
		} else {
			m.emailed = true
		}
	}
	if err := m.reload(); err != nil {
		m.err = err
	}
	m.screen = screenResult
	return m, nil
}

func (m *Model) cancelReset() error {
	return m.store.DeleteAuthTokensForUser(context.Background(), m.target.ID, forum.TokenPasswordReset)
}

func (m Model) startSetPassword() (tea.Model, tea.Cmd) {
	m.title = fmt.Sprintf("New password for %s", m.target.Username)
	m.help = "At least 12 characters. Saving ends that account's sessions. Enter saves, Esc goes back."
	m.inputs = []textinput.Model{passwordInput("New password"), passwordInput("Confirm password")}
	m.focus = 0
	cmd := m.inputs[0].Focus()
	m.submit = func(m *Model) error {
		password, confirm := m.inputs[0].Value(), m.inputs[1].Value()
		if message := forum.ValidatePassword(password); message != "" {
			return errors.New(message)
		}
		if password != confirm {
			return errors.New("the two passwords do not match")
		}
		hash, err := forum.HashPassword(password)
		if err != nil {
			return err
		}
		ctx := context.Background()
		if err := m.store.SetPassword(ctx, m.target.ID, hash); err != nil {
			return err
		}
		if err := m.store.DeleteSessionsForUser(ctx, m.target.ID); err != nil {
			return err
		}
		m.flash = fmt.Sprintf("Password updated for %s. Their sessions were ended.", m.target.Username)
		return nil
	}
	m.screen = screenInput
	return m, cmd
}

func (m Model) startCreateOwner() (tea.Model, tea.Cmd) {
	m.title = "Create an owner"
	m.help = "Owners manage boards, groups, members, and settings. Enter saves, Esc cancels."
	m.inputs = []textinput.Model{textInput("Username"), passwordInput("Password"), passwordInput("Confirm password")}
	m.focus = 0
	cmd := m.inputs[0].Focus()
	m.submit = func(m *Model) error {
		username := m.inputs[0].Value()
		password, confirm := m.inputs[1].Value(), m.inputs[2].Value()
		if message := forum.ValidateCredentials(username, password); message != "" {
			return errors.New(message)
		}
		if password != confirm {
			return errors.New("the two passwords do not match")
		}
		hash, err := forum.HashPassword(password)
		if err != nil {
			return err
		}
		if err := m.store.CreateOwner(context.Background(), username, hash); err != nil {
			return err
		}
		m.flash = fmt.Sprintf("Owner %q created.", username)
		return nil
	}
	m.screen = screenInput
	return m, cmd
}

func (m Model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			if m.target.ID != 0 {
				m.screen = screenActions
			} else {
				m.screen = screenList
			}
			return m, nil
		case "tab", "down":
			return m, m.focusInput(m.focus + 1)
		case "shift+tab", "up":
			return m, m.focusInput(m.focus - 1)
		case "enter":
			if err := m.submit(&m); err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.target = forum.MemberReset{}
			if err := m.reload(); err != nil {
				m.err = err
			}
			m.screen = screenList
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m *Model) focusInput(next int) tea.Cmd {
	if next < 0 {
		next = len(m.inputs) - 1
	}
	if next >= len(m.inputs) {
		next = 0
	}
	m.inputs[m.focus].Blur()
	m.focus = next
	return m.inputs[m.focus].Focus()
}

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y", "enter":
		if err := m.onYes(&m); err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.flash = "Reset link cancelled."
		if err := m.reload(); err != nil {
			m.err = err
		}
		m.screen = screenList
	case "n", "N", "esc", "q":
		m.screen = screenActions
	}
	return m, nil
}

func (m Model) updateResult(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "e":
		if m.mailer().Enabled() && m.target.Email != "" && !m.emailed {
			message := mail.Message{
				To:      m.target.Email,
				Subject: "Choose a new password for " + m.opts.SiteName,
				Body:    resetBody(m.target.Username, m.opts.SiteName, m.link),
			}
			if err := m.mailer().Send(context.Background(), message); err != nil {
				m.err = err
			} else {
				m.emailed = true
				m.err = nil
			}
		}
	case "q", "esc", "enter", " ":
		m.screen = screenList
	}
	return m, nil
}

func (m Model) listView() string {
	var b []string
	b = append(b, titleStyle.Render(fmt.Sprintf("%s admin", m.opts.SiteName)))
	b = append(b, helpStyle.Render(fmt.Sprintf("%d account(s)", len(m.members))))
	b = append(b, m.table.View())
	b = append(b, m.notices()...)
	b = append(b, helpStyle.Render("↑/↓ move · enter manage · n new owner · r refresh · q quit"))
	return lipgloss.JoinVertical(lipgloss.Left, b...)
}

func (m Model) actionsView() string {
	var b []string
	b = append(b, titleStyle.Render("Manage "+m.target.Username))
	for i, choice := range m.items {
		if i == m.cursor {
			b = append(b, cursorStyle.Render("> "+choice.label))
		} else {
			b = append(b, "  "+choice.label)
		}
	}
	b = append(b, m.notices()...)
	b = append(b, helpStyle.Render("↑/↓ choose · enter select · esc back"))
	return lipgloss.JoinVertical(lipgloss.Left, b...)
}

func (m Model) inputView() string {
	var b []string
	b = append(b, titleStyle.Render(m.title))
	for i, input := range m.inputs {
		marker := "  "
		if i == m.focus {
			marker = cursorStyle.Render("> ")
		}
		b = append(b, marker+input.View())
	}
	b = append(b, m.notices()...)
	b = append(b, helpStyle.Render(m.help))
	return lipgloss.JoinVertical(lipgloss.Left, b...)
}

func (m Model) confirmView() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render("Please confirm"),
		m.confirm,
		"",
		helpStyle.Render("y confirm · n or esc cancel"),
	)
}

func (m Model) resultView() string {
	lines := []string{
		titleStyle.Render("Reset link for " + m.target.Username),
		"",
		m.link,
		"",
	}
	if m.emailed {
		lines = append(lines, okStyle.Render("Emailed to "+m.target.Email+"."))
	} else if m.mailer().Enabled() && m.target.Email != "" {
		lines = append(lines, helpStyle.Render("Press e to email it to "+m.target.Email+", or copy the link."))
	}
	lines = append(lines, helpStyle.Render("It works once and expires in 24 hours. Copy it now; it cannot be shown again."))
	lines = append(lines, m.notices()...)
	lines = append(lines, helpStyle.Render("esc back to the list"))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m Model) notices() []string {
	var out []string
	if m.err != nil {
		out = append(out, errorStyle.Render("Error: "+m.err.Error()))
	}
	if m.flash != "" {
		out = append(out, okStyle.Render(m.flash))
	}
	return out
}

func textInput(label string) textinput.Model {
	input := textinput.New()
	input.Prompt = label + ": "
	input.CharLimit = 254
	input.Width = 40
	return input
}

func passwordInput(label string) textinput.Model {
	input := textInput(label)
	input.EchoMode = textinput.EchoPassword
	input.CharLimit = 72
	return input
}

func resetBody(name, site, link string) string {
	return fmt.Sprintf(`Hello %s,

An owner of %s made a link so you can choose a new password. Open it here:

%s

The link works once and expires in 24 hours.

If you did not ask for this, you can ignore this message: your password has not changed.
`, name, site, link)
}

func atLeast(a, b int) int {
	if a > b {
		return a
	}
	return b
}
