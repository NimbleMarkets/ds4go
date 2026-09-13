package webtool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// visit_page applies the same scheme and destination policy as fetch_image
// before the browser is touched: file://, chrome://, loopback, private, and
// link-local destinations are refused unless AllowPrivateFetch, so a model
// steered by page content cannot read local files or reach services behind
// the host through the browser instead of through fetch_image.
func TestVisitPageAppliesTheDestinationPolicy(t *testing.T) {
	strict := NewWebHelper(Config{HomeDir: t.TempDir()})
	for _, u := range []string{"file:///etc/hosts", "chrome://version", "ftp://example.com/", "not a url", ""} {
		_, err := strict.VisitPage(context.Background(), u)
		if err == nil || !strings.Contains(err.Error(), "http") {
			t.Errorf("VisitPage(%q) = %v, want a refusal naming http(s)", u, err)
		}
	}
	for _, u := range []string{"http://127.0.0.1:1/", "http://localhost/", "http://10.0.0.5/", "http://169.254.169.254/latest/meta-data", "http://[::1]/"} {
		_, err := strict.VisitPage(context.Background(), u)
		if err == nil || !strings.Contains(err.Error(), "private") {
			t.Errorf("VisitPage(%q) = %v, want a refusal naming private addresses", u, err)
		}
	}

	// The registered tool goes through the same gate.
	tool := strict.VisitPageTool()
	args, _ := json.Marshal(map[string]string{"url": "file:///etc/passwd"})
	if _, err := tool.Handler(context.Background(), args); err == nil || !strings.Contains(err.Error(), "http") {
		t.Errorf("visit_page tool: %v, want a refusal naming http(s)", err)
	}

	// AllowPrivateFetch lifts the address policy but never the scheme policy.
	open := NewWebHelper(Config{HomeDir: t.TempDir(), AllowPrivateFetch: true})
	if _, err := open.VisitPage(context.Background(), "file:///etc/hosts"); err == nil || !strings.Contains(err.Error(), "http") {
		t.Errorf("VisitPage(file://) with AllowPrivateFetch = %v, want a refusal naming http(s)", err)
	}
}
