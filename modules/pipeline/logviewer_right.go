// Copyright © 2026 Harness Inc.
// SPDX-License-Identifier: Apache-2.0

package pipeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/harness/harness-cli/pkg/tui"
)

type rightTab int

const (
	tabLogs rightTab = iota
	tabDetails
	tabInputs
	tabOutputs
)

type tabDef struct {
	label string
	key   string
	tab   rightTab
}

var tabDefs = []tabDef{
	{"Logs", "l", tabLogs},
	{"Details", "d", tabDetails},
	{"Inputs", "i", tabInputs},
	{"Outputs", "o", tabOutputs},
}

func (m logViewModel) rightPanelWidth() int {
	w := m.width - leftPanelWidth - 1
	if w < 0 {
		return 0
	}
	return w
}

func (m logViewModel) renderRightPanel() string {
	var b strings.Builder
	b.WriteString(m.renderTabBar() + "\n")
	b.WriteString(m.st.divider.Render(strings.Repeat("─", m.rightPanelWidth())) + "\n")
	switch m.activeTab {
	case tabLogs:
		b.WriteString(m.renderTabLogs())
	case tabDetails:
		b.WriteString(m.renderTabDetails())
	case tabInputs:
		b.WriteString(m.renderTabInputs())
	case tabOutputs:
		b.WriteString(m.renderTabOutputs())
	}
	return b.String()
}

func (m logViewModel) renderTabBar() string {
	st := m.st
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.CLITextMuted))
	tabs := make([]string, len(tabDefs))
	for i, td := range tabDefs {
		hotkey := keyStyle.Render("(" + td.key + ")")
		if td.tab == m.activeTab {
			tabs[i] = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIAccent)).Render(td.label) + " " + hotkey
		} else {
			tabs[i] = st.dim.Render(td.label) + " " + hotkey
		}
	}
	return strings.Join(tabs, st.divider.Render("  ·  "))
}

func (m logViewModel) renderTabLogs() string {
	if m.loading {
		return m.spin.View() + " loading…"
	}
	return m.vp.View()
}

func (m logViewModel) renderTabDetails() string {
	return m.vp.View()
}

func (m logViewModel) renderTabInputs() string {
	return m.vp.View()
}

func (m logViewModel) renderTabOutputs() string {
	return m.vp.View()
}

func (m *logViewModel) renderDetailsContent(step *lvStep) string {
	st := m.st
	if step == nil {
		return st.dim.Render("(no step selected)")
	}

	label := func(s string) string {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(tui.CLIText)).Render(s)
	}
	val := func(s string) string { return st.normal.Render(s) }
	dim := func(s string) string { return st.dim.Render(s) }

	fmtTs := func(ms int64) string {
		if ms == 0 {
			return dim("—")
		}
		return val(time.UnixMilli(ms).Local().Format("1/2/2006, 3:04:05 PM"))
	}

	var b strings.Builder
	b.WriteString(label("Started at:") + "  " + fmtTs(step.startTs) + "\n")
	b.WriteString(label("Ended at:  ") + "  " + fmtTs(step.endTs) + "\n")

	dur := dim("—")
	if step.startTs > 0 && step.endTs > step.startTs {
		d := time.Duration(step.endTs-step.startTs) * time.Millisecond
		dur = val(d.Round(time.Second).String())
	}
	b.WriteString(label("Duration:  ") + "  " + dur + "\n")

	// Parse timeout from stepParameters JSON.
	timeout := dim("—")
	if step.inputs != "" {
		var params map[string]any
		if err := json.Unmarshal([]byte(step.inputs), &params); err == nil {
			if t, ok := params["timeout"]; ok {
				timeout = val(fmt.Sprintf("%v", t))
			}
		}
	}
	b.WriteString(label("Timeout:   ") + "  " + timeout + "\n")

	if len(step.delegates) > 0 {
		b.WriteString("\n" + label("Delegates:") + "\n")
		for _, d := range step.delegates {
			b.WriteString("  " + val(d) + "\n")
		}
	}

	return b.String()
}

func prettyJSON(raw string, st lvStyles) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return st.dim.Render(raw)
	}
	return buf.String()
}

// syncViewportForTab updates the viewport content for non-log tabs.
// Call this whenever the active tab or selected step changes.
func (m *logViewModel) syncViewportForTab() {
	if m.activeTab == tabLogs {
		return
	}
	st := m.st
	var step *lvStep
	for i := range m.steps {
		if m.steps[i].logKey == m.selectedKey {
			step = &m.steps[i]
			break
		}
	}

	switch m.activeTab {
	case tabDetails:
		m.vp.SetContent(m.renderDetailsContent(step))
	case tabInputs:
		if step == nil || step.inputs == "" {
			m.vp.SetContent(st.dim.Render("(no inputs)"))
		} else {
			m.vp.SetContent(prettyJSON(step.inputs, st))
		}
	case tabOutputs:
		if step == nil || step.outputs == "" {
			m.vp.SetContent(st.dim.Render("(no outputs)"))
		} else {
			m.vp.SetContent(prettyJSON(step.outputs, st))
		}
	}
	m.vp.GotoTop()
}
