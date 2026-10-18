package tui

import (
	"slices"

	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/stack"
)

// actionFor returns the rig command a key runs, given the current report
// and the selected site. ok is false if the key runs nothing.
//
// Toggles look at the report: a running worker or service is stopped,
// a stopped one is started. Site names come from rig's own status output
// but are validated again before they become arguments.
func actionFor(k Key, rep stack.Report, sel int) (args []string, ok bool) {
	switch k {
	case KeyMail:
		return toggleService(rep, "mail"), true
	case KeyS3:
		return toggleService(rep, "s3"), true
	}

	if sel < 0 || sel >= len(rep.Sites) {
		return nil, false
	}
	s := rep.Sites[sel]
	if site.ValidateName(s.Name) != nil {
		return nil, false
	}
	toggleWorker := func(kind string) []string {
		if slices.Contains(s.Workers, kind) {
			return []string{"worker", "stop", s.Name, kind}
		}
		return []string{"worker", "start", s.Name, kind}
	}
	switch k {
	case KeyOpen:
		return []string{"open", s.Name}, true
	case KeyQueue:
		return toggleWorker("queue"), true
	case KeySchedule:
		return toggleWorker("schedule"), true
	case KeyReverb:
		return toggleWorker("reverb"), true
	}
	return nil, false
}

// toggleService returns "rig <name> stop" if the service runs, else start.
func toggleService(rep stack.Report, name string) []string {
	for _, s := range rep.Services {
		if s.Name == name && s.State == "active" {
			return []string{name, "stop"}
		}
	}
	return []string{name, "start"}
}
