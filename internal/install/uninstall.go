package install

import "github.com/vojislavd/rig/internal/db"

// BuildUninstall returns the plan that removes everything rig install
// creates. It is also the rollback after a failed install: every path is
// owned by rig alone, so removing it can't hurt anything else.
//
// Every step tolerates "already gone", so it is safe to run more than
// once and after a half-finished install. The CA in the user's home is
// left alone; once untrusted it has no effect.
func BuildUninstall() *Plan {
	return &Plan{Title: "rig uninstall", Actions: []Action{
		{
			Kind: Run, Argv: []string{"/usr/bin/systemctl", "disable", "--now", NatUnitName},
			Why: "stop the port redirect (its stop step deletes the rule) and remove it from boot", Optional: true,
		},
		{
			Kind: Run, Argv: []string{"/usr/bin/nft", "delete", "table", "inet", natTable},
			Why: "make sure the redirect rule is gone even if the service was not running", Optional: true,
		},
		{Kind: RemoveFile, Path: natUnitPath, Why: "delete the redirect service file"},
		{Kind: RemoveFile, Path: natConfPath, Why: "delete the redirect rule file"},
		{Kind: RemoveDir, Path: "/etc/rig", Why: "delete rig's config folder", Optional: true},
		{Kind: RemoveFile, Path: TrustAnchorPath, Why: "stop trusting rig's CA"},
		{
			Kind: Run, Argv: []string{"/usr/bin/mariadb"}, Stdin: db.AdminRemoveSQL(), StdinShown: db.AdminRemoveSQL(),
			Why: "remove rig's MariaDB account and helper procedures (site databases are kept)", Optional: true,
		},
		{
			Kind: Run, Argv: []string{updateCATrust},
			Why: "rebuild the system trust store without rig's CA",
		},
		{
			Kind: Run, Argv: []string{"/usr/bin/systemctl", "daemon-reload"},
			Why: "make systemd forget the removed unit file",
		},
	}}
}

// BuildRollback is the uninstall plan with every step optional, used to
// clean up after a failed install: it removes as much as it can and
// never stops early.
func BuildRollback() *Plan {
	p := BuildUninstall()
	p.Title = "rig install rollback"
	for i := range p.Actions {
		p.Actions[i].Optional = true
	}
	return p
}
