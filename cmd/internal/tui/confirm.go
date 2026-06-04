// Package tui provides terminal UI helpers.
package tui

import (
	"fmt"
	"io"

	"charm.land/bubbletea/v2"
)

type ConfirmResult int

const (
	ConfirmYes ConfirmResult = iota
	ConfirmNo
	ConfirmCancelled
)

// Confirm displays a Yes/No dialog and returns the user's selection.
// defaultYes sets which button is initially highlighted. If in or out are nil,
// the default tea program I/O (stdin/stdout) is used.
func Confirm(prompt string, defaultYes bool, in io.Reader, out io.Writer) (ConfirmResult, error) {
	m := confirmModel{prompt: prompt, selectYes: defaultYes, result: ConfirmCancelled}
	var progOpts []tea.ProgramOption
	if in != nil {
		progOpts = append(progOpts, tea.WithInput(in))
	}
	if out != nil {
		progOpts = append(progOpts, tea.WithOutput(out))
	}
	final, err := tea.NewProgram(m, progOpts...).Run()
	if err != nil {
		return ConfirmCancelled, err
	}
	return final.(confirmModel).result, nil
}

type confirmModel struct {
	prompt    string
	selectYes bool
	result    ConfirmResult
	done      bool
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c":
		m.result = ConfirmCancelled
		m.done = true
		return m, tea.Interrupt
	case "q", "esc":
		m.result = ConfirmCancelled
		m.done = true
		return m, tea.Quit
	case "y", "Y":
		m.result = ConfirmYes
		m.done = true
		return m, tea.Quit
	case "n", "N":
		m.result = ConfirmNo
		m.done = true
		return m, tea.Quit
	case "left", "right", "h", "l", "tab", "shift+tab":
		m.selectYes = !m.selectYes
	case "enter":
		if m.selectYes {
			m.result = ConfirmYes
		} else {
			m.result = ConfirmNo
		}
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	yes, no := UnselectedBox.Render("Yes"), UnselectedBox.Render("No")
	if m.selectYes {
		yes = SelectedStyle.Render("Yes")
	} else {
		no = SelectedStyle.Render("No")
	}
	help := MutedStyle.Render("←/→ toggle  y/n select  enter submit  esc cancel")
	return tea.NewView(fmt.Sprintf("\n%s\n\n  %s  %s\n\n%s\n",
		TitleStyle.Render(m.prompt), yes, no, help))
}
