package env

import (
	"strings"
	"testing"
)

// laravelDefault is the database part of Laravel's default .env.
const laravelDefault = `APP_NAME=Laravel
APP_KEY=base64:abc=
APP_URL=http://localhost

DB_CONNECTION=sqlite
# DB_HOST=127.0.0.1
# DB_PORT=3306
# DB_DATABASE=laravel
# DB_USERNAME=root
# DB_PASSWORD=

SESSION_DRIVER=database
`

func rigVars() []Var {
	return []Var{
		{Key: "APP_URL", Value: "https://foo.localhost"},
		{Key: "DB_CONNECTION", Value: "mariadb"},
		{Key: "DB_HOST", Value: "127.0.0.1"},
		{Key: "DB_PORT", Value: "3306"},
		{Key: "DB_DATABASE", Value: "rig_foo"},
		{Key: "DB_USERNAME", Value: "rig_foo"},
		{Key: "DB_PASSWORD", Value: "s3cretS3cretS3cretS3cretS3cret12", Secret: true},
	}
}

func TestApplyLaravelDefault(t *testing.T) {
	out, changes, err := Apply([]byte(laravelDefault), rigVars())
	if err != nil {
		t.Fatal(err)
	}
	want := `APP_NAME=Laravel
APP_KEY=base64:abc=
APP_URL=https://foo.localhost

DB_CONNECTION=mariadb
DB_HOST=127.0.0.1
DB_PORT=3306
DB_DATABASE=rig_foo
DB_USERNAME=rig_foo
DB_PASSWORD=s3cretS3cretS3cretS3cretS3cret12

SESSION_DRIVER=database
`
	if string(out) != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	if len(changes) != 7 {
		t.Errorf("%d changes, want 7", len(changes))
	}
	for _, c := range changes {
		if c.Key == "DB_HOST" && c.Before != "commented" {
			t.Errorf("DB_HOST state %q, want commented", c.Before)
		}
		if c.Key == "DB_PASSWORD" && (c.New != "(hidden)" || strings.Contains(c.Old+c.New, "s3cret")) {
			t.Errorf("password shown in changes: %+v", c)
		}
	}
}

func TestApplyKeepsOtherLinesAndAppendsMissing(t *testing.T) {
	in := "# my comment\nFOO=bar\nexport DB_HOST=\"db.example\"\n"
	out, changes, err := Apply([]byte(in), []Var{{Key: "DB_HOST", Value: "127.0.0.1"}, {Key: "DB_PORT", Value: "3306"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "# my comment\nFOO=bar\nDB_HOST=127.0.0.1\n\n# rig\nDB_PORT=3306\n"
	if string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if changes[0].Old != "db.example" || changes[1].Before != "missing" {
		t.Errorf("changes = %+v", changes)
	}
}

func TestApplyNoChangeWhenAlreadySet(t *testing.T) {
	in := "DB_PORT=3306\n"
	out, changes, err := Apply([]byte(in), []Var{{Key: "DB_PORT", Value: "3306"}})
	if err != nil || string(out) != in || len(changes) != 0 {
		t.Errorf("Apply = %q, %v, %v", out, changes, err)
	}
}

func TestApplyEmptyFile(t *testing.T) {
	out, _, err := Apply(nil, []Var{{Key: "DB_PORT", Value: "3306"}})
	if err != nil || string(out) != "# rig\nDB_PORT=3306\n" {
		t.Errorf("Apply(empty) = %q, %v", out, err)
	}
}

func TestApplyRejectsUnsafe(t *testing.T) {
	for _, v := range []Var{
		{Key: "DB_HOST", Value: "x\nAPP_DEBUG=true"},
		{Key: "DB_HOST", Value: "a b"},
		{Key: "DB_HOST", Value: `"q"`},
		{Key: "DB_HOST", Value: "x#y"},
		{Key: "DB_HOST", Value: "$(id)"},
		{Key: "lower", Value: "x"},
		{Key: "BAD KEY", Value: "x"},
	} {
		if _, _, err := Apply([]byte(""), []Var{v}); err == nil {
			t.Errorf("Apply accepted %+v", v)
		}
	}
}

func TestApplyOnlyIfEmpty(t *testing.T) {
	in := "A=keep\nB=\n"
	out, changes, err := Apply([]byte(in), []Var{
		{Key: "A", Value: "new", OnlyIfEmpty: true},
		{Key: "B", Value: "filled", OnlyIfEmpty: true},
		{Key: "C", Value: "added", OnlyIfEmpty: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "A=keep\nB=filled\n\n# rig\nC=added\n" {
		t.Errorf("got %q", out)
	}
	if len(changes) != 2 {
		t.Errorf("changes = %+v, want B and C only", changes)
	}
}

func TestApplyHideOld(t *testing.T) {
	_, changes, err := Apply([]byte("MAIL_PASSWORD=realsecret\n"), []Var{{Key: "MAIL_PASSWORD", Value: "null", HideOld: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Old != "(hidden)" || changes[0].New != "null" {
		t.Errorf("changes = %+v", changes)
	}
}
