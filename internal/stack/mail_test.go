package stack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestStartStopMail(t *testing.T) {
	st, _ := testStack(t)
	sd := st.S.(*fakeSystemd)
	if got := st.MailState(); got != "stopped" {
		t.Errorf("state before start = %q", got)
	}
	if err := st.StartMail(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(st.mailDir())
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("mail folder: %v, mode %v", err, info.Mode().Perm())
	}
	unit, _ := os.ReadFile(filepath.Join(st.L.Units, "rig-mailpit.service"))
	if !strings.Contains(string(unit), "BindPaths="+st.mailDir()+"\n") {
		t.Error("unit doesn't bind the mail folder")
	}
	if !slices.Contains(sd.calls, "start rig-mailpit.service") {
		t.Errorf("calls = %v", sd.calls)
	}
	if got := st.MailState(); got != "active" {
		t.Errorf("state = %q", got)
	}
	sd.calls = nil
	if err := st.StopMail(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sd.calls, []string{"stop rig-mailpit.service", "daemon-reload"}) {
		t.Errorf("stop calls = %v", sd.calls)
	}
	if st.MailState() != "stopped" {
		t.Error("still running after stop")
	}
	if err := st.StopMail(); err != nil {
		t.Errorf("stopping twice: %v", err)
	}
}
