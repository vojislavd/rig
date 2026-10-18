package db

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vojislavd/rig/internal/fsutil"
)

// Client talks to MariaDB as rigadmin.
//
// Security: the password comes only from the login file, given to the
// mariadb tools with --defaults-extra-file. It never appears in command
// arguments, where other users could read it with ps. Database names come
// from Name, so they are always rig_<letters, digits, underscores>.
type Client struct {
	Cnf     string           // ~/.config/rig/mariadb.cnf
	Backups string           // ~/.local/share/rig/backups
	Now     func() time.Time // for backup file names
}

// query runs SQL (sent on stdin) and returns its output.
func (c *Client) query(sql string) (string, error) {
	cmd := exec.Command("/usr/bin/mariadb", "--defaults-extra-file="+c.Cnf, "--batch", "--skip-column-names")
	cmd.Stdin = strings.NewReader(sql)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("mariadb: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// List returns the names of all rig site databases.
func (c *Client) List() ([]string, error) {
	out, err := c.query("SHOW DATABASES LIKE 'rig\\_%';")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// Exists reports whether the site's database exists.
func (c *Client) Exists(siteName string) (bool, error) {
	name, err := Name(siteName)
	if err != nil {
		return false, err
	}
	names, err := c.List()
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

// CreateSite creates (or resets the password of) the site's database and
// user, and returns the database/user name and a new random password.
// The password is shown to MariaDB only as a hash; the caller puts the
// plaintext in the project's .env and nowhere else.
func (c *Client) CreateSite(siteName string) (name, password string, err error) {
	if name, err = Name(siteName); err != nil {
		return "", "", err
	}
	if password, err = NewPassword(); err != nil {
		return "", "", err
	}
	hash, err := Hash(password)
	if err != nil {
		return "", "", err
	}
	if _, err := c.query(fmt.Sprintf("CALL rigsys.create_site('%s', '%s');", name, hash)); err != nil {
		return "", "", err
	}
	return name, password, nil
}

// Dump writes a gzipped SQL dump of the site's database to the backups
// folder and returns its path. The file is private (0600) and appears
// only once complete.
func (c *Client) Dump(siteName string) (string, error) {
	name, err := Name(siteName)
	if err != nil {
		return "", err
	}
	if err := fsutil.EnsurePrivateDir(c.Backups); err != nil {
		return "", err
	}
	path := filepath.Join(c.Backups, fmt.Sprintf("%s-%s.sql.gz", name, c.Now().Format("20060102-150405")))

	tmp, err := os.CreateTemp(c.Backups, ".dump-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	gz := gzip.NewWriter(tmp)
	cmd := exec.Command("/usr/bin/mariadb-dump", "--defaults-extra-file="+c.Cnf,
		"--single-transaction", "--routines", "--triggers", "--events", "--databases", name)
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = gz, &errOut
	runErr := cmd.Run()
	closeErr := errors.Join(gz.Close(), tmp.Sync(), tmp.Close())
	if runErr != nil {
		return "", fmt.Errorf("mariadb-dump %s: %w: %s", name, runErr, strings.TrimSpace(errOut.String()))
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// DropSite dumps the site's database, then drops the database and its
// user. If the dump fails, nothing is dropped. A site without a database
// only has its user removed. Returns the backup path ("" if none).
func (c *Client) DropSite(siteName string) (backup string, err error) {
	if _, err := Name(siteName); err != nil {
		return "", err
	}
	exists, err := c.Exists(siteName)
	if err != nil {
		return "", err
	}
	if exists {
		if backup, err = c.Dump(siteName); err != nil {
			return "", fmt.Errorf("backup failed, nothing dropped: %w", err)
		}
	}
	return backup, c.Drop(siteName)
}

// Drop removes the site's database and user without a backup. Callers
// must have just made one with Dump (rig rm does: back up, unlink, drop).
func (c *Client) Drop(siteName string) error {
	name, err := Name(siteName)
	if err != nil {
		return err
	}
	_, err = c.query(fmt.Sprintf("CALL rigsys.drop_site('%s');", name))
	return err
}

// readDump returns the SQL inside a gzipped dump (used by tests).
func readDump(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(gz)
	return string(b), err
}
