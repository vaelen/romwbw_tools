// Copyright 2026 Andrew C. Young <andrew@vaelen.org>
// SPDX-License-Identifier: MIT

// Package tui is a handful of inline (non-fullscreen) bubbletea prompts:
// pick one, pick several in order, reorder, and type a value. Each prompt
// collapses to a one-line summary when answered, so a session reads as a
// short transcript of choices.
package tui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var ErrAborted = errors.New("cancelled")

var (
	bold   = lipgloss.NewStyle().Bold(true)
	accent = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	faint  = lipgloss.NewStyle().Faint(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

const window = 12 // rows of a list shown at once

// IsTerminal reports whether stdin is interactive, i.e. whether a TUI can run.
func IsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

type Item struct{ Label, Detail string }

func run(m tea.Model) (tea.Model, error) {
	fm, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, err
	}
	return fm, nil
}

func done(title, answer string) string {
	return green.Render("✓ ") + bold.Render(title) + " " + answer + "\n"
}

// ---- list: single or ordered multi select --------------------------------

type list struct {
	title    string
	items    []Item
	cursor   int
	top      int
	multi    bool
	max      int
	picked   []int // multi: indexes in the order chosen
	msg      string
	finished bool
	aborted  bool
}

func (m *list) Init() tea.Cmd { return nil }

func (m *list) pos(i int) int {
	for p, v := range m.picked {
		if v == i {
			return p
		}
	}
	return -1
}

func (m *list) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	m.msg = ""
	switch k.String() {
	case "ctrl+c", "esc", "q":
		m.aborted = true
		return m, tea.Quit
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= window
	case "pgdown":
		m.cursor += window
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.items) - 1
	case " ", "x":
		if m.multi {
			if p := m.pos(m.cursor); p >= 0 {
				m.picked = append(m.picked[:p], m.picked[p+1:]...)
			} else if len(m.picked) >= m.max {
				m.msg = fmt.Sprintf("at most %d can be chosen", m.max)
			} else {
				m.picked = append(m.picked, m.cursor)
			}
		}
	case "enter":
		m.finished = true
		return m, tea.Quit
	}
	m.cursor = max(0, min(m.cursor, len(m.items)-1))
	if m.cursor < m.top {
		m.top = m.cursor
	} else if m.cursor >= m.top+window {
		m.top = m.cursor - window + 1
	}
	return m, nil
}

func (m *list) View() string {
	if m.finished {
		if !m.multi {
			return done(m.title, m.items[m.cursor].Label)
		}
		names := make([]string, len(m.picked))
		for i, p := range m.picked {
			names[i] = m.items[p].Label
		}
		if len(names) == 0 {
			names = []string{"(none)"}
		}
		return done(m.title, strings.Join(names, ", "))
	}
	if m.aborted {
		return ""
	}
	width := 0
	for _, it := range m.items {
		width = max(width, len(it.Label))
	}
	var b strings.Builder
	b.WriteString(bold.Render("? "+m.title) + "\n")
	end := min(len(m.items), m.top+window)
	if m.top > 0 {
		b.WriteString(faint.Render(fmt.Sprintf("    ↑ %d more", m.top)) + "\n")
	}
	for i := m.top; i < end; i++ {
		it := m.items[i]
		mark := "  "
		if i == m.cursor {
			mark = accent.Render("› ")
		}
		box := ""
		if m.multi {
			if p := m.pos(i); p >= 0 {
				box = accent.Render(fmt.Sprintf("[%d] ", p+1))
			} else {
				box = "[ ] "
			}
		}
		label := fmt.Sprintf("%-*s", width, it.Label)
		if i == m.cursor {
			label = accent.Render(label)
		}
		b.WriteString(mark + box + label + "  " + faint.Render(it.Detail) + "\n")
	}
	if end < len(m.items) {
		b.WriteString(faint.Render(fmt.Sprintf("    ↓ %d more", len(m.items)-end)) + "\n")
	}
	if m.multi {
		b.WriteString(faint.Render(fmt.Sprintf("  ↑/↓ move · space toggle (numbers show order, %d max) · enter done · esc quit", m.max)) + "\n")
	} else {
		b.WriteString(faint.Render("  ↑/↓ move · enter choose · esc quit") + "\n")
	}
	if m.msg != "" {
		b.WriteString(red.Render("  "+m.msg) + "\n")
	}
	return b.String()
}

// Select asks for one item and returns its index.
func Select(title string, items []Item, cursor int) (int, error) {
	fm, err := run(&list{title: title, items: items, cursor: cursor, top: max(0, cursor-window+1)})
	if err != nil {
		return 0, err
	}
	m := fm.(*list)
	if m.aborted {
		return 0, ErrAborted
	}
	return m.cursor, nil
}

// MultiSelect asks for up to max items; the result is in the order picked.
func MultiSelect(title string, items []Item, preselected []int, maxPicks int) ([]int, error) {
	fm, err := run(&list{title: title, items: items, multi: true, max: maxPicks, picked: append([]int(nil), preselected...)})
	if err != nil {
		return nil, err
	}
	m := fm.(*list)
	if m.aborted {
		return nil, ErrAborted
	}
	return m.picked, nil
}

// Confirm is a yes/no Select.
func Confirm(title string, def bool) (bool, error) {
	c := 1
	if def {
		c = 0
	}
	i, err := Select(title, []Item{{Label: "Yes"}, {Label: "No"}}, c)
	return i == 0, err
}

// ---- reorder ---------------------------------------------------------------

type reorder struct {
	title    string
	labels   []string
	order    []int
	cursor   int
	grabbed  bool
	finished bool
	aborted  bool
}

func (m *reorder) Init() tea.Cmd { return nil }

func (m *reorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	move := 0
	switch k.String() {
	case "ctrl+c", "esc", "q":
		m.aborted = true
		return m, tea.Quit
	case "up", "k":
		move = -1
	case "down", "j":
		move = 1
	case " ":
		m.grabbed = !m.grabbed
	case "enter":
		m.finished = true
		return m, tea.Quit
	}
	to := m.cursor + move
	if move != 0 && to >= 0 && to < len(m.order) {
		if m.grabbed {
			m.order[m.cursor], m.order[to] = m.order[to], m.order[m.cursor]
		}
		m.cursor = to
	}
	return m, nil
}

func (m *reorder) View() string {
	if m.finished {
		names := make([]string, len(m.order))
		for i, o := range m.order {
			names[i] = m.labels[o]
		}
		return done(m.title, strings.Join(names, ", "))
	}
	if m.aborted {
		return ""
	}
	var b strings.Builder
	b.WriteString(bold.Render("? "+m.title) + "\n")
	for i, o := range m.order {
		line := fmt.Sprintf("%d. %s", i, m.labels[o])
		switch {
		case i == m.cursor && m.grabbed:
			b.WriteString(accent.Render("≡ "+line) + "\n")
		case i == m.cursor:
			b.WriteString(accent.Render("› "+line) + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	b.WriteString(faint.Render("  ↑/↓ move · space grab/drop (then ↑/↓ moves the item) · enter done · esc quit") + "\n")
	return b.String()
}

// Reorder lets the user rearrange labels; it returns the new order as indexes.
func Reorder(title string, labels []string) ([]int, error) {
	order := make([]int, len(labels))
	for i := range order {
		order[i] = i
	}
	fm, err := run(&reorder{title: title, labels: labels, order: order})
	if err != nil {
		return nil, err
	}
	m := fm.(*reorder)
	if m.aborted {
		return nil, ErrAborted
	}
	return m.order, nil
}

// ---- text input ------------------------------------------------------------

type input struct {
	title    string
	ti       textinput.Model
	finished bool
	aborted  bool
}

func (m *input) Init() tea.Cmd { return textinput.Blink }

func (m *input) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "enter":
			m.finished = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m *input) View() string {
	if m.finished {
		return done(m.title, m.ti.Value())
	}
	if m.aborted {
		return ""
	}
	return bold.Render("? "+m.title) + "\n  " + m.ti.View() + "\n" +
		faint.Render("  enter accept · esc quit") + "\n"
}

// Input asks for a line of text, pre-filled with def.
func Input(title, def string) (string, error) {
	ti := textinput.New()
	ti.SetValue(def)
	ti.Focus()
	ti.Width = 60
	fm, err := run(&input{title: title, ti: ti})
	if err != nil {
		return "", err
	}
	m := fm.(*input)
	if m.aborted {
		return "", ErrAborted
	}
	v := strings.TrimSpace(m.ti.Value())
	if v == "" {
		v = def
	}
	return v, nil
}
