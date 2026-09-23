package webtool

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
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

// The destination policy is applied to the page that is extracted, not only
// to the page the tab first landed on. A page that passes the landing check
// and then moves itself to a private address (a timed location change) while
// visit_page waits on the consent step and dynamic scrolling is refused, and
// nothing from the private page comes back.
func TestVisitPageRefusesAPageThatMovesToAPrivateAddressBeforeExtraction(t *testing.T) {
	const landing = "http://public.test/"
	const moved = "http://192.168.1.1/"

	// The mock page: its location is read wherever the expression starts
	// with location.href, and its timer fires (moving it to the router
	// admin page) once visit_page has passed the landing check and is on
	// the consent step.
	var mu sync.Mutex
	loc := landing
	current := func() string {
		mu.Lock()
		defer mu.Unlock()
		return loc
	}
	evaluate := func(expr string) string {
		switch {
		case expr == "document.readyState":
			return "complete"
		case expr == "location.href":
			return current()
		case strings.HasPrefix(expr, "location.href+'\\n'+document.readyState"):
			return current() + "\ncomplete\n100"
		case strings.Contains(expr, "accept all"):
			mu.Lock()
			loc = moved
			mu.Unlock()
			return ""
		case strings.Contains(expr, "## Content"):
			body := "# Router admin\n\nURL: " + current() + "\n\n## Content\n\nadmin password: hunter2"
			if strings.HasPrefix(expr, "location.href+'\\n'+") {
				return current() + "\n" + body
			}
			return body
		default: // the dynamic-scroll probe
			return "scroll skipped"
		}
	}

	port, stop := startMockCDP(t, evaluate)
	defer stop()

	w := NewWebHelper(Config{Port: port, HomeDir: t.TempDir(), ChromePath: "mock-chrome"})
	w.browserAllowed = true
	w.lookupIP = func(host string) ([]net.IP, error) { return []net.IP{net.IPv4(203, 0, 113, 5)}, nil }

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := w.VisitPage(ctx, landing)
	if err == nil || !strings.Contains(err.Error(), "private") || !strings.Contains(err.Error(), moved) {
		t.Errorf("VisitPage after the page moved to %s: err = %v, want a refusal naming the private address", moved, err)
	}
	if out != "" {
		t.Errorf("VisitPage returned content from the private page: %q", out)
	}
}
