package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vojislavd/rig/internal/stack"
)

func TestParseKeys(t *testing.T) {
	got := parseKeys([]byte("jk\x1b[A\x1b[Bq\x03o\r123msrx"))
	want := []Key{KeyDown, KeyUp, KeyUp, KeyDown, KeyQuit, KeyQuit, KeyOpen, KeyOpen, KeyQueue, KeySchedule, KeyReverb, KeyMail, KeyS3, KeyRefresh}
	if !slices.Equal(got, want) {
		t.Errorf("parseKeys = %v, want %v", got, want)
	}
}

func testReport() stack.Report {
	return stack.Report{
		Install: stack.InstallStatus{PortRedirect: "active", CATrusted: true},
		Services: []stack.ServiceStatus{
			{Name: "nginx", State: "active"}, {Name: "mail", State: "active", OnDemand: true}, {Name: "s3", State: "stopped", OnDemand: true},
		},
		Sites: []stack.SiteStatus{
			{SiteInfo: stack.SiteInfo{Name: "alpha", URL: "https://alpha.localhost", CertExpiry: time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)}, Database: "rig_alpha", Workers: []string{"queue"}},
			{SiteInfo: stack.SiteInfo{Name: "beta", URL: "https://beta.localhost"}, S3: true, ReverbPort: 6001, Workers: []string{}},
		},
	}
}

func TestActionFor(t *testing.T) {
	rep := testReport()
	cases := []struct {
		k    Key
		sel  int
		want []string
	}{
		{KeyOpen, 1, []string{"open", "beta"}},
		{KeyQueue, 0, []string{"worker", "stop", "alpha", "queue"}}, // running -> stop
		{KeyQueue, 1, []string{"worker", "start", "beta", "queue"}}, // stopped -> start
		{KeyReverb, 1, []string{"worker", "start", "beta", "reverb"}},
		{KeyMail, 0, []string{"mail", "stop"}},
		{KeyS3, 0, []string{"s3", "start"}},
	}
	for _, c := range cases {
		got, ok := actionFor(c.k, rep, c.sel)
		if !ok || !slices.Equal(got, c.want) {
			t.Errorf("actionFor(%v, sel %d) = %v, %v; want %v", c.k, c.sel, got, ok, c.want)
		}
	}
	if _, ok := actionFor(KeyOpen, rep, 5); ok {
		t.Error("action for a selection outside the list")
	}
	// A name that isn't a valid site name never becomes an argument.
	rep.Sites[0].Name = "x; rm -rf ~"
	if args, ok := actionFor(KeyOpen, rep, 0); ok {
		t.Errorf("built %v from an invalid site name", args)
	}
}

func TestRender(t *testing.T) {
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	lines := render(screen{rep: testReport(), sel: 0, width: 200, now: now, message: "rig mail stop: Mailpit stopped.", messageOK: true})
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		"rig  ·  2 sites  ·  all good",
		"● nginx", "● mail", "○ s3", "● port redirect", "● CA trusted",
		"▌ alpha", "77 days", "6001", "queue",
		"https://alpha.localhost", "db rig_alpha", // detail line of the selection
		"1 stop queue", "2 start schedule", "m stop mail", "s start s3", "q quit",
		"✓ rig mail stop: Mailpit stopped.",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("render missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "\x1b[") {
		t.Error("colors used although color is off")
	}

	// Problems show in the header; a failed action gets a cross.
	rep := testReport()
	rep.Install.CATrusted = false
	rep.Services[2].State = "failed"
	rep.Sites[1].CertIssue = "expires in 3 days"
	all = strings.Join(render(screen{rep: rep, width: 200, message: "rig s3 start: failed"}), "\n")
	for _, want := range []string{"3 problems", "✕ CA trusted", "✕ s3", "renew", "✗ rig s3 start"} {
		if !strings.Contains(all, want) {
			t.Errorf("render missing %q:\n%s", want, all)
		}
	}
}

func TestRenderNarrowFooter(t *testing.T) {
	lines := render(screen{rep: testReport(), width: 60})
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "quit") || strings.Contains(l, "select") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("footer on %d lines at width 60, want 2:\n%s", n, strings.Join(lines, "\n"))
	}
	for _, l := range lines {
		if visibleLen(l) > 60 {
			t.Errorf("line wider than the terminal: %q", l)
		}
	}
}

func TestRenderColumnsAlignWithColor(t *testing.T) {
	plain := render(screen{rep: testReport(), width: 200})
	colored := render(screen{rep: testReport(), width: 200, color: true})
	for i := range plain {
		if visibleLen(plain[i]) != visibleLen(colored[i]) {
			t.Errorf("line %d: %d visible chars with color, %d without", i, visibleLen(colored[i]), visibleLen(plain[i]))
		}
	}
}

func TestCut(t *testing.T) {
	if got := cut("abcdef", 3); got != "abc" {
		t.Errorf("cut = %q", got)
	}
	colored := "\x1b[32mactive\x1b[0m and more"
	got := cut(colored, 6)
	if !strings.HasPrefix(got, "\x1b[32mactive") || strings.Contains(got, "and") {
		t.Errorf("cut with colors = %q", got)
	}
	if got := cut("čaj čaj", 3); got != "čaj" {
		t.Errorf("cut UTF-8 = %q", got)
	}
}
