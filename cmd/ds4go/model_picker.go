package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/NimbleMarkets/ds4go/internal/models"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type modelPicker struct {
	title  string
	models []models.Model
	cursor int
	chosen string
	cancel bool
}

func newModelPicker(title string, list []models.Model) modelPicker {
	return modelPicker{title: title, models: list}
}

func (m modelPicker) Init() tea.Cmd {
	return nil
}

func (m modelPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.cancel = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.models)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.models) > 0 {
				m.chosen = m.models[m.cursor].Alias
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m modelPicker) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#39FFB6")).Render(m.title)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#7D8590"))
	active := lipgloss.NewStyle().Foreground(lipgloss.Color("#39FFB6")).Bold(true)
	primary := lipgloss.NewStyle().Foreground(lipgloss.Color("#C9D1D9"))

	// Fixed-width columns so aliases, sizes, RAM, and flags line up.
	aliasStyle := lipgloss.NewStyle().Width(14)
	sizeStyle := lipgloss.NewStyle().Width(10).Align(lipgloss.Right)
	ramStyle := lipgloss.NewStyle().Width(13)
	flagsStyle := lipgloss.NewStyle().Width(18)

	var b strings.Builder
	b.WriteString(title)
	b.WriteString("\n\n")
	for i, model := range m.models {
		marker := "  "
		style := primary
		if i == m.cursor {
			marker = "▸ "
			style = active
		}
		status := "available"
		if model.Installed {
			status = "installed"
		} else if model.Partial {
			status = "partial " + formatPartialModel(model.PartialBytes, model.SizeGB)
		}
		if model.Default {
			status += ", default"
		}
		colAlias := aliasStyle.Render(model.Alias)
		colSize := sizeStyle.Render(fmt.Sprintf("%.1f GiB", model.SizeGB))
		colRAM := ramStyle.Render(model.RecommendedRAM)
		colFlags := flagsStyle.Render(modelFlags(model))
		b.WriteString(style.Render(marker + colAlias + " " + colSize))
		b.WriteString(muted.Render("  " + colRAM + "  " + colFlags + "  " + status))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(muted.Render("j/k or arrows move  enter select  esc cancel"))
	v := tea.NewView(b.String())
	return v
}

func pickModelAlias(title string, list []models.Model, in io.Reader, out io.Writer) (string, error) {
	if len(list) == 0 {
		return "", fmt.Errorf("no models available")
	}
	p := tea.NewProgram(newModelPicker(title, list), tea.WithInput(in), tea.WithOutput(out))
	result, err := p.Run()
	if err != nil {
		return "", err
	}
	picker, ok := result.(modelPicker)
	if !ok {
		return "", fmt.Errorf("model picker returned unexpected state")
	}
	if picker.cancel || picker.chosen == "" {
		return "", fmt.Errorf("cancelled")
	}
	return picker.chosen, nil
}
