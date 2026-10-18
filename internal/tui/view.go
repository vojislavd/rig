package tui

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vojislavd/rig/internal/stack"
)

// screen is what the dashboard shows at one moment.
type screen struct {
	rep       stack.Report
	sel       int
	message   string // result of the last action
	messageOK bool   // false: the action failed
	pending   bool   // the action is still running
	width     int
	color     bool
	now       time.Time
}

// ANSI styles. Only the basic 16 colors, so every terminal theme maps
// them to its own palette.
const (
	bold   = "1"
	dim    = "2"
	red    = "31"
	green  = "32"
	yellow = "33"
	cyan   = "36"
)

// render returns the screen as lines, each cut to the terminal width.
func render(s screen) []string {
	style := func(code, text string) string {
		if !s.color || code == "" {
			return text
		}
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
	dot := func(state string) string {
		switch state {
		case "active":
			return style(green, "●")
		case "failed", "missing":
			return style(red, "✕")
		default:
			return style(dim, "○")
		}
	}

	var lines []string

	// Header: name, site count, overall health.
	health := style(green, "all good")
	if n := problems(s.rep); n == 1 {
		health = style(red, "1 problem")
	} else if n > 1 {
		health = style(red, strconv.Itoa(n)+" problems")
	}
	sites := fmt.Sprintf("%d sites", len(s.rep.Sites))
	if len(s.rep.Sites) == 1 {
		sites = "1 site"
	}
	lines = append(lines, " "+style(bold, "rig")+style(dim, "  ·  ")+sites+style(dim, "  ·  ")+health, "")

	// Services, then what rig install set up.
	var items []string
	for _, sv := range s.rep.Services {
		items = append(items, dot(sv.State)+" "+sv.Name)
	}
	trusted := "missing"
	if s.rep.Install.CATrusted {
		trusted = "active"
	}
	items = append(items, "  "+dot(s.rep.Install.PortRedirect)+" port redirect", dot(trusted)+" CA trusted")
	lines = append(lines, " "+strings.Join(items, "   "), "")

	if len(s.rep.Sites) == 0 {
		lines = append(lines, " No sites yet. Run "+style(bold, "rig link")+" inside a project in ~/code.")
	} else {
		lines = append(lines, siteTable(s, style)...)
		if s.sel >= 0 && s.sel < len(s.rep.Sites) {
			st := s.rep.Sites[s.sel]
			detail := []string{st.URL}
			if st.Dir != "" {
				detail = append(detail, shortHome(st.Dir))
			}
			if st.Database != "" && st.Database != "?" {
				detail = append(detail, "db "+st.Database)
			}
			if st.FilesURL != "" {
				detail = append(detail, "files "+st.FilesURL)
			}
			lines = append(lines, "", "   "+style(dim, strings.Join(detail, "  ·  ")))
		}
	}

	lines = append(lines, "")
	lines = append(lines, footer(s, style)...)
	if s.message != "" {
		mark := style(green, "✓")
		if s.pending {
			mark = style(dim, "…")
		} else if !s.messageOK {
			mark = style(red, "✗")
		}
		lines = append(lines, "", " "+mark+" "+s.message)
	}
	for i, l := range lines {
		lines[i] = cut(l, s.width)
	}
	return lines
}

// cell is one table cell: plain text (for measuring) and its style.
type cell struct {
	text, style string
}

func siteTable(s screen, style func(code, text string) string) []string {
	header := []string{"SITE", "CERT", "DB", "S3", "REVERB", "WORKERS"}
	rows := [][]cell{}
	for _, st := range s.rep.Sites {
		cert, certStyle := certText(st, s.now)
		db := cell{"·", dim}
		switch st.Database {
		case "?":
			db = cell{"?", dim}
		case "":
		default:
			db = cell{"✓", green}
		}
		s3 := cell{"·", dim}
		if st.S3 {
			s3 = cell{"✓", green}
		}
		reverb := cell{"·", dim}
		if st.ReverbPort != 0 {
			reverb = cell{strconv.Itoa(st.ReverbPort), ""}
		}
		workers := cell{"·", dim}
		if len(st.Workers) > 0 {
			workers = cell{strings.Join(st.Workers, " "), green}
		}
		rows = append(rows, []cell{{st.Name, ""}, {cert, certStyle}, db, s3, reverb, workers})
	}

	// Column widths from the plain text; colors are added after padding,
	// so escape codes never throw the alignment off.
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], utf8.RuneCountInString(c.text))
		}
	}
	pad := func(text string, w int) string {
		return text + strings.Repeat(" ", w-utf8.RuneCountInString(text)+3)
	}

	var h strings.Builder
	for i, t := range header {
		h.WriteString(pad(t, widths[i]))
	}
	out := []string{"   " + style(dim, strings.TrimRight(h.String(), " "))}
	for ri, r := range rows {
		var b strings.Builder
		selected := ri == s.sel
		if selected {
			b.WriteString(" " + style(cyan, "▌") + " ")
		} else {
			b.WriteString("   ")
		}
		for i, c := range r {
			code := c.style
			if i == 0 && selected {
				code = bold + ";" + cyan
			}
			text := pad(c.text, widths[i])
			if i == len(r)-1 {
				text = c.text
			}
			// Style only the text, not the padding after it.
			b.WriteString(style(code, c.text) + text[len(c.text):])
		}
		out = append(out, b.String())
	}
	return out
}

// certText shows how long a site certificate has left.
func certText(s stack.SiteStatus, now time.Time) (string, string) {
	if s.CertIssue != "" {
		return "renew", red
	}
	if s.CertExpiry.IsZero() {
		return "·", dim
	}
	if now.IsZero() {
		return s.CertExpiry.Format(time.DateOnly), ""
	}
	days := int(s.CertExpiry.Sub(now).Hours() / 24)
	if days < 30 {
		return fmt.Sprintf("%d days", days), yellow
	}
	return fmt.Sprintf("%d days", days), ""
}

// problems counts what needs attention: rig install's parts, the always-on
// services, failed on-demand services or workers, and certificates.
func problems(rep stack.Report) int {
	n := 0
	if rep.Install.PortRedirect != "active" {
		n++
	}
	if !rep.Install.CATrusted {
		n++
	}
	for _, sv := range rep.Services {
		if sv.State == "failed" || (!sv.OnDemand && sv.State != "active") {
			n++
		}
	}
	for _, st := range rep.Sites {
		if st.CertIssue != "" {
			n++
		}
	}
	return n
}

// footer lists the keys. Toggles say what they will do for the selected
// site right now ("start queue" or "stop queue").
func footer(s screen, style func(code, text string) string) []string {
	key := func(k, label string) string { return style(bold, k) + " " + style(dim, label) }
	verb := func(running bool) string {
		if running {
			return "stop "
		}
		return "start "
	}
	var workers []string
	if s.sel >= 0 && s.sel < len(s.rep.Sites) {
		workers = s.rep.Sites[s.sel].Workers
	}
	svc := func(name string) bool {
		for _, sv := range s.rep.Services {
			if sv.Name == name {
				return sv.State == "active"
			}
		}
		return false
	}
	var parts []string
	if len(s.rep.Sites) > 0 {
		parts = append(parts,
			key("↑↓", "select"),
			key("o", "open"),
			key("1", verb(slices.Contains(workers, "queue"))+"queue"),
			key("2", verb(slices.Contains(workers, "schedule"))+"schedule"),
			key("3", verb(slices.Contains(workers, "reverb"))+"reverb"),
		)
	}
	global := []string{
		key("m", verb(svc("mail"))+"mail"),
		key("s", verb(svc("s3"))+"s3"),
		key("r", "refresh"),
		key("q", "quit"),
	}
	// One line if it fits, otherwise site keys and global keys apart.
	one := " " + strings.Join(append(parts, global...), "   ")
	if s.width <= 0 || visibleLen(one) <= s.width || len(parts) == 0 {
		return []string{one}
	}
	return []string{" " + strings.Join(parts, "   "), " " + strings.Join(global, "   ")}
}

// visibleLen counts the characters of line that show, skipping escape
// codes.
func visibleLen(line string) int {
	n := 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b {
			j := strings.IndexByte(line[i:], 'm')
			if j < 0 {
				break
			}
			i += j + 1
			continue
		}
		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
		n++
	}
	return n
}

// shortHome shows a path with ~ for the home folder.
func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// cut shortens line to width visible characters, skipping over escape
// codes so colors aren't cut in half.
func cut(line string, width int) string {
	if width <= 0 {
		return line
	}
	var b strings.Builder
	visible := 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b { // copy the whole escape code
			j := strings.IndexByte(line[i:], 'm')
			if j < 0 {
				break
			}
			b.WriteString(line[i : i+j+1])
			i += j + 1
			continue
		}
		r, n := utf8.DecodeRuneInString(line[i:])
		if visible >= width {
			break
		}
		b.WriteRune(r)
		visible++
		i += n
	}
	if strings.Contains(line, "\x1b[") {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
