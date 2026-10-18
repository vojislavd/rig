package stack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/mailpit"
	"github.com/vojislavd/rig/internal/php"
	"github.com/vojislavd/rig/internal/site"
	"github.com/vojislavd/rig/internal/worker"
)

// LogSource is where a log lives: a file, or a systemd unit's journal.
// Exactly one field is set.
type LogSource struct {
	File string
	Unit string
}

// LogTargets is the help text for "rig logs".
const LogTargets = "nginx, nginx:access, php, mail, s3, <site>, <site>:queue|schedule|reverb"

// LogSource maps a "rig logs" target to its log. Every
// path or unit name is built from fixed names or a validated site name,
// never from the target text itself.
func (st *Stack) LogSource(target string) (LogSource, error) {
	switch target {
	case "nginx":
		return LogSource{File: filepath.Join(st.L.Logs, "nginx-error.log")}, nil
	case "nginx:access":
		return LogSource{File: filepath.Join(st.L.Logs, "nginx-access.log")}, nil
	case "php":
		return LogSource{File: filepath.Join(st.L.Logs, "php"+php.Default.ID()+"-fpm.log")}, nil
	case "mail":
		return LogSource{Unit: mailpit.UnitName}, nil
	case "s3":
		return LogSource{Unit: garage.UnitName}, nil
	}

	name, kind, hasKind := strings.Cut(target, ":")
	if err := site.ValidateName(name); err != nil {
		return LogSource{}, fmt.Errorf("unknown log %q (one of: %s)", target, LogTargets)
	}
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return LogSource{}, err
	}
	s, ok := r.Get(name)
	if !ok {
		return LogSource{}, fmt.Errorf("%s: %w", name, site.ErrNotFound)
	}
	if hasKind {
		if err := worker.CheckKind(kind); err != nil {
			return LogSource{}, err
		}
		return LogSource{Unit: worker.UnitName(name, kind)}, nil
	}
	file, err := laravelLog(s.Dir)
	return LogSource{File: file}, err
}

// laravelLog returns the site's newest storage/logs/laravel*.log (the
// "single" channel writes laravel.log, "daily" writes
// laravel-YYYY-MM-DD.log). With no log yet it returns laravel.log, which
// tail -F waits for.
//
// Security: only regular files directly in storage/logs count, and
// storage/logs itself must resolve to a folder inside the project. A
// symlink there could otherwise make rig show any file you can read
// under a misleading name.
func laravelLog(dir string) (string, error) {
	logs := filepath.Join(dir, "storage", "logs")
	real, err := filepath.EvalSymlinks(logs)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s does not exist", logs)
	}
	if err != nil {
		return "", err
	}
	if realDir, err := filepath.EvalSymlinks(dir); err != nil || !strings.HasPrefix(real, realDir+"/") {
		return "", fmt.Errorf("%s points outside the project", logs)
	}

	newest, newestTime := filepath.Join(real, "laravel.log"), int64(0)
	matches, _ := filepath.Glob(filepath.Join(real, "laravel*.log"))
	for _, m := range matches {
		info, err := os.Lstat(m)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if t := info.ModTime().UnixNano(); t > newestTime {
			newest, newestTime = m, t
		}
	}
	return newest, nil
}
