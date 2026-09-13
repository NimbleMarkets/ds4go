package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/NimbleMarkets/ds4go/internal/models"
)

func TestRenderLoadedEngines(t *testing.T) {
	var buf bytes.Buffer
	renderLoadedEngines(&buf, []loadedEngine{
		{PID: 31324, Process: "ds4go", Files: []models.LoadedFile{
			{Path: "/m/ds4flash.gguf", Alias: "q2-imatrix", Role: models.RoleModel},
			{Path: "/m/DeepSeek-V4-Flash-MTP-Q4K-Q8_0-F32.gguf", Alias: "mtp", Role: models.RoleMTP},
		}},
		{PID: 99, Process: "dwarfgalaxy", Files: []models.LoadedFile{{Path: "/srv/custom.gguf", Role: models.RoleUnknown}}},
	})
	out := buf.String()
	for _, want := range []string{"LOADED", "31324", "ds4go", "q2-imatrix (model)", "mtp (mtp)", "99", "dwarfgalaxy", "/srv/custom.gguf"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	buf.Reset()
	renderLoadedEngines(&buf, nil)
	if !strings.Contains(buf.String(), "No engines loaded") {
		t.Errorf("empty = %q", buf.String())
	}
}
