package models

import "sort"

// LoadedRole says what part a GGUF plays for a running engine.
type LoadedRole string

const (
	// RoleModel is the language checkpoint.
	RoleModel LoadedRole = "model"
	// RoleMTP is a speculative-decoding companion (MTP or DSpark support).
	RoleMTP LoadedRole = "mtp"
	// RoleVision is a vision encoder.
	RoleVision LoadedRole = "vision"
	// RoleUnknown is a file the catalog does not describe.
	RoleUnknown LoadedRole = ""
)

// LoadedFile is one GGUF a process has open, mapped onto the catalog.
type LoadedFile struct {
	Path  string     `json:"path"`
	Alias string     `json:"alias,omitempty"`
	Role  LoadedRole `json:"role,omitempty"`
}

// ClassifyLoadedFiles maps open model files onto catalog aliases and roles.
// The active-model link resolves to the checkpoint it points at
// (ModelForPath follows both symlinks and hard-link identity). Results are
// ordered model, then companions, then unknown, so the checkpoint leads.
func ClassifyLoadedFiles(paths []string) []LoadedFile {
	encoders := map[string]bool{}
	for _, m := range Curated() {
		if m.Encoder != "" {
			encoders[m.Encoder] = true
		}
	}
	out := make([]LoadedFile, 0, len(paths))
	for _, p := range paths {
		f := LoadedFile{Path: p}
		if m, ok := ModelForPath(p); ok {
			f.Alias = m.Alias
			switch {
			case encoders[m.Alias]:
				f.Role = RoleVision
			case m.Optional:
				f.Role = RoleMTP
			default:
				f.Role = RoleModel
			}
		}
		out = append(out, f)
	}
	rank := map[LoadedRole]int{RoleModel: 0, RoleMTP: 1, RoleVision: 2, RoleUnknown: 3}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Role] < rank[out[j].Role] })
	return out
}
