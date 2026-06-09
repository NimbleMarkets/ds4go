package install

// nimbleStyle is a placeholder for Nimble Markets terminal styling.
//
// The current implementation intentionally returns plain text. Keeping the
// calls centralized makes it easy to add color or richer terminal treatment
// later without touching installer behavior.
type nimbleStyle struct{}

func defaultNimbleStyle() nimbleStyle {
	return nimbleStyle{}
}

func (nimbleStyle) Action(s string) string {
	return s + ":"
}

func (nimbleStyle) Asset(s string) string {
	return s
}

func (nimbleStyle) Header(s string) string {
	return s
}

func (nimbleStyle) Label(s string) string {
	return s
}

func (nimbleStyle) Selected(s string) string {
	return s
}
