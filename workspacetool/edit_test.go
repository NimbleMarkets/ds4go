package workspacetool

import (
	"strings"
	"testing"
)

// Ported from ds4 upstream 7624c68 "Add ds4-agent edit regression tests"
// (see docs/ROADMAP.md). The newline-boundary cases guard the a9f486b fix
// that strips a leading newline/CR from the tail needle.
func TestFindEditSpanUpstreamRegressions(t *testing.T) {
	const makefileData = "CFLAGS = -Wall -Wextra -g\n" +
		"LDFLAGS =\n" +
		"\n" +
		"all: bc\n" +
		"\n" +
		"bc: main.c\n" +
		"\t$(CC) $(CFLAGS) -o bc main.c $(LDFLAGS)\n" +
		"\n" +
		"clean:\n" +
		"\trm -f bc\n"
	const makefileOld = "CFLAGS = -Wall -Wextra -g\n" +
		"LDFLAGS =\n" +
		"\n" +
		"all: bc\n" +
		"\n" +
		"bc: main.c\n" +
		"\t$(CC) $(CFLAGS) -o bc main.c $(LDFLAGS)\n" +
		"\n" +
		"[upto]\n" +
		"clean:\n"

	cases := []struct {
		name         string
		data, old    string
		wantSpan     string
		wantAnchored bool
		wantErr      string
	}{
		{
			name:         "upto tail newline is not part of anchor",
			data:         makefileData,
			old:          makefileOld,
			wantSpan:     strings.TrimSuffix(makefileData, "\trm -f bc\n"),
			wantAnchored: true,
		},
		{
			name:    "upto requires tail after newline strip",
			data:    "head\nbody\ntail\n",
			old:     "head\n[upto]\n",
			wantErr: "must include a unique tail anchor",
		},
		{
			name:    "upto requires tail after crlf strip",
			data:    "head\nbody\ntail\n",
			old:     "head\n[upto]\n\r\n",
			wantErr: "must include a unique tail anchor",
		},
		{
			name:         "tail directly after head newline boundary",
			data:         "a\nb\nc\n",
			old:          "a\n[upto]\nb\nc\n",
			wantSpan:     "a\nb\nc\n",
			wantAnchored: true,
		},
		{
			name:    "head anchor not found",
			data:    "a\nb\n",
			old:     "missing\n[upto]\nb\n",
			wantErr: "old head anchor not found",
		},
		{
			name:    "head anchor not unique",
			data:    "x\ny\nx\ny\n",
			old:     "x\n[upto]\ny\ny\n",
			wantErr: "old head anchor is not unique",
		},
		{
			name:    "tail anchor not found after head",
			data:    "a\nb\nc\n",
			old:     "c\n[upto]\na\n",
			wantErr: "old tail anchor not found",
		},
		{
			name:    "tail anchor not unique",
			data:    "h\nt\nt\n",
			old:     "h\n[upto]\nt\n",
			wantErr: "old tail anchor is not unique",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, anchored, err := findEditSpan(tc.data, tc.old)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("findEditSpan: %v", err)
			}
			if anchored != tc.wantAnchored {
				t.Fatalf("anchored = %v, want %v", anchored, tc.wantAnchored)
			}
			if got := tc.data[start:end]; got != tc.wantSpan {
				t.Fatalf("span = %q, want %q", got, tc.wantSpan)
			}
		})
	}
}
