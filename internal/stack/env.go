package stack

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/vojislavd/rig/internal/env"
	"github.com/vojislavd/rig/internal/fsutil"
	"github.com/vojislavd/rig/internal/garage"
	"github.com/vojislavd/rig/internal/site"
)

// DBCreator creates a site's database and user (db.Client in real use).
type DBCreator interface {
	CreateSite(siteName string) (name, password string, err error)
}

// EnvPreview is what rig env will change, shown before anything happens.
type EnvPreview struct {
	Path        string       `json:"path"`
	FromExample bool         `json:"from_example"` // .env is new, started from .env.example
	Changes     []env.Change `json:"changes"`
	OldMode     string       `json:"old_mode,omitempty"` // set if the mode changes to 0600
}

const maxEnvSize = 1 << 20

// Env points the site's .env at its database and URL. It shows the
// planned changes to confirm (secrets hidden); only if that returns true
// does it reset the site's database password and write .env (0600).
// With a nil dbc, only APP_URL is set and no database is touched (for
// projects without one).
//
// The database password is stored nowhere else: .env is its only home.
func (st *Stack) Env(name string, dbc DBCreator, confirm func(EnvPreview) bool) (EnvPreview, bool, error) {
	r, err := site.Load(st.L.Registry())
	if err != nil {
		return EnvPreview{}, false, err
	}
	s, ok := r.Get(name)
	if !ok {
		return EnvPreview{}, false, fmt.Errorf("%w: %q", site.ErrNotFound, name)
	}
	path := filepath.Join(s.Dir, ".env")

	content, oldMode, exists, err := readEnv(path)
	if err != nil {
		return EnvPreview{}, false, err
	}
	fromExample := false
	if !exists {
		example, _, exampleExists, err := readEnv(filepath.Join(s.Dir, ".env.example"))
		if err != nil {
			return EnvPreview{}, false, err
		}
		content, fromExample = example, exampleExists
	}

	// Preview with a stand-in password; the real one is made only after
	// confirmation, so declining changes nothing in MariaDB either.
	var s3 *s3Creds
	if s.S3 {
		id, secret, err := st.s3Credentials(name)
		if err != nil {
			return EnvPreview{}, false, fmt.Errorf("%s uses S3: %w", name, err)
		}
		s3 = &s3Creds{id: id, secret: secret}
	}
	dbName := "rig_" + strings.ReplaceAll(name, "-", "_")
	vars := envVars(s, dbName, strings.Repeat("x", 40), dbc != nil, s3)
	_, changes, err := env.Apply(content, vars)
	if err != nil {
		return EnvPreview{}, false, err
	}
	preview := EnvPreview{Path: path, FromExample: fromExample, Changes: changes}
	if exists && oldMode != 0o600 {
		preview.OldMode = fmt.Sprintf("%04o", oldMode)
	}
	if !confirm(preview) {
		return preview, false, nil
	}

	if dbc != nil {
		gotName, password, err := dbc.CreateSite(name)
		if err != nil {
			return preview, false, err
		}
		vars = envVars(s, gotName, password, true, s3)
	}
	out, _, err := env.Apply(content, vars)
	if err != nil {
		return preview, false, err
	}
	if err := fsutil.WriteFileAtomic(path, out, 0o600); err != nil {
		return preview, false, fmt.Errorf("database password was reset but .env could not be written (run rig env again): %w", err)
	}
	return preview, true, nil
}

// envVars lists the keys rig sets: APP_URL and the Mailpit keys always,
// the database keys if withDB, and the Reverb keys once the site has a
// Reverb port. Laravel
// talks to Reverb through nginx (https://<site>.localhost:443); Reverb
// itself listens on 127.0.0.1:<port>.
// s3Creds is a site's Garage access key.
type s3Creds struct{ id, secret string }

func envVars(s site.Site, dbName, password string, withDB bool, s3 *s3Creds) []env.Var {
	host := site.Host(s.Name)
	vars := []env.Var{
		{Key: "APP_URL", Value: "https://" + host},
		// All mail goes to Mailpit (rig mail start); nothing leaves the
		// machine. "null" is how Laravel's .env spells "not set".
		{Key: "MAIL_MAILER", Value: "smtp"},
		{Key: "MAIL_SCHEME", Value: "null"},
		{Key: "MAIL_HOST", Value: "127.0.0.1"},
		{Key: "MAIL_PORT", Value: "1025"},
		{Key: "MAIL_USERNAME", Value: "null", HideOld: true},
		{Key: "MAIL_PASSWORD", Value: "null", HideOld: true},
	}
	if withDB {
		vars = append(vars,
			env.Var{Key: "DB_CONNECTION", Value: "mariadb"},
			env.Var{Key: "DB_HOST", Value: "127.0.0.1"},
			env.Var{Key: "DB_PORT", Value: "3306"},
			env.Var{Key: "DB_DATABASE", Value: dbName},
			env.Var{Key: "DB_USERNAME", Value: dbName},
			env.Var{Key: "DB_PASSWORD", Value: password, Secret: true},
		)
	}
	if s3 != nil {
		// Laravel's default disk becomes Garage. Requests go through nginx
		// at https://files.<site>.localhost, so temporaryUrl() links work
		// in the browser too.
		vars = append(vars,
			env.Var{Key: "FILESYSTEM_DISK", Value: "s3"},
			env.Var{Key: "AWS_ACCESS_KEY_ID", Value: s3.id},
			env.Var{Key: "AWS_SECRET_ACCESS_KEY", Value: s3.secret, Secret: true},
			env.Var{Key: "AWS_DEFAULT_REGION", Value: garage.Region},
			env.Var{Key: "AWS_BUCKET", Value: site.Bucket(s.Name)},
			env.Var{Key: "AWS_ENDPOINT", Value: "https://" + site.FilesHost(s.Name)},
			env.Var{Key: "AWS_USE_PATH_STYLE_ENDPOINT", Value: "true"},
		)
	}
	if s.ReverbPort != 0 {
		vars = append(vars, reverbCredentials()...)
		vars = append(vars,
			env.Var{Key: "REVERB_HOST", Value: host},
			env.Var{Key: "REVERB_PORT", Value: "443"},
			env.Var{Key: "REVERB_SCHEME", Value: "https"},
			env.Var{Key: "REVERB_SERVER_HOST", Value: "127.0.0.1"},
			env.Var{Key: "REVERB_SERVER_PORT", Value: strconv.Itoa(s.ReverbPort)},
		)
	}
	return vars
}

// readEnv reads a .env-style file. A missing file is not an error. A
// symlink or non-regular file is refused: rig won't read or write through
// a link that could point outside the project.
func readEnv(path string) (content []byte, mode fs.FileMode, exists bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("%s is not a regular file (symlink?); refusing to use it", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxEnvSize+1))
	if err != nil {
		return nil, 0, false, err
	}
	if len(data) > maxEnvSize {
		return nil, 0, false, fmt.Errorf("%s is larger than %d bytes", path, maxEnvSize)
	}
	return data, info.Mode().Perm(), true, nil
}

// reverbCredentials returns random Reverb app credentials in the format
// "php artisan reverb:install" uses: a 6-digit app id and 20-character
// lowercase key and secret. They are set only where .env has none, so
// existing credentials are never replaced.
func reverbCredentials() []env.Var {
	return []env.Var{
		{Key: "REVERB_APP_ID", Value: randomString("0123456789", 6), OnlyIfEmpty: true},
		{Key: "REVERB_APP_KEY", Value: randomString("abcdefghijklmnopqrstuvwxyz0123456789", 20), OnlyIfEmpty: true, Secret: true},
		{Key: "REVERB_APP_SECRET", Value: randomString("abcdefghijklmnopqrstuvwxyz0123456789", 20), OnlyIfEmpty: true, Secret: true},
	}
}

// randomString returns n characters chosen with crypto/rand.
func randomString(chars string, n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(chars)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // the kernel's random source failing is not recoverable
		}
		b[i] = chars[v.Int64()]
	}
	return string(b)
}
