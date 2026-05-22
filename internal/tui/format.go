package tui

import (
	"fmt"
	"strings"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

// FormatModelBytes formats a byte count into a human-readable string (e.g. "3.5 GiB").
func FormatModelBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for next := div * unit; n >= next && exp < 4; next *= unit {
		div = next
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// FormatPartialModel formats the download progress of a partially downloaded model.
func FormatPartialModel(partialBytes int64, sizeGiB float64) string {
	if sizeGiB <= 0 {
		return FormatModelBytes(partialBytes)
	}
	currentGiB := float64(partialBytes) / (1024 * 1024 * 1024)
	pct := currentGiB / sizeGiB * 100
	if pct > 999 {
		pct = 999
	}
	return fmt.Sprintf("%.1f / ~%.1f GiB %.1f%%", currentGiB, sizeGiB, pct)
}

// ModelFlags returns a comma-separated string of flags/attributes for a model.
func ModelFlags(model models.Model) string {
	var flags []string
	if model.Imatrix {
		flags = append(flags, "imatrix")
	}
	if model.Legacy {
		flags = append(flags, "legacy")
	}
	if model.Optional {
		flags = append(flags, "mtp")
	}
	return strings.Join(flags, ", ")
}
