# rig

A local development environment for Laravel on Arch Linux, built with security first.

`rig link` in a project folder serves it at `https://<folder>.localhost` with a trusted
certificate, its own PHP-FPM pool and its own MariaDB database. Everything runs natively
(no containers) as **systemd user services**: after a one-time setup, nothing rig starts
runs as root and nothing listens beyond loopback.

## Features

- **HTTPS for every site**: `https://foo.localhost`, plus `*.foo.localhost`. No `/etc/hosts`
  entries and no DNS server: `*.localhost` already resolves to the local machine
  ([RFC 6761](https://www.rfc-editor.org/rfc/rfc6761)).
- **Its own CA, restricted to `.localhost`**: even a leaked CA key can't fake a certificate
  for a real website.
- **Rootless stack**: nginx and PHP-FPM run as your user, in sandboxed systemd user units.
- **One database and user per site**, with a random password.
- **`.env` wiring**: `APP_URL`, `DB_*`, `MAIL_*`, `AWS_*` and `REVERB_*` are filled in for
  you, after showing a diff. Other keys are left alone.
- **Workers on demand**: `queue`, `schedule` and `reverb`, started only when you ask.
- **Mail catching** with [Mailpit](https://mailpit.axllent.org/) and **S3 storage** with
  [Garage](https://garagehq.deuxfleurs.fr/), both on demand.
- **`rig doctor`**: checks the whole chain and repairs rig's files (`--fix`); `--security`
  audits listeners, file permissions and leftovers of old setups.
- **A terminal dashboard**: run `rig` with no arguments.
- **`--json`** output for scripts.

## Requirements

- **Arch Linux** (or an Arch-based distro) with systemd.
- **Go 1.27.2 or newer** to build.
- Packages:

  | Package | Needed for |
  |---|---|
  | `nginx`, `php`, `php-fpm` | serving sites (PHP 8.5) |
  | `mariadb` (service running) | databases |
  | `nftables`, `ca-certificates` | `rig install` |
  | `xdg-utils` | `rig open`, `rig mail open` |
  | `garage` | optional: `rig s3` |
  | `mailpit` (AUR) | optional: `rig mail` |

- Projects live in **`~/code/<name>`**. The folder name is the site name: lowercase
  letters, digits and single hyphens (`my-app`), at most 60 characters.
- MariaDB must listen on loopback only: set `bind-address=127.0.0.1` in
  `/etc/my.cnf.d/server.cnf` (Arch's default accepts network connections).
  `rig doctor --security` fails if it doesn't.

## Install

```sh
git clone https://github.com/vojislavd/rig.git
cd rig
go build -trimpath -o ~/.local/bin/rig ./cmd/rig
```

With `~/.local/bin` on your `PATH`, run the one-time system setup. Preview it first: it
prints every file it will create and every command it will run as root.

```sh
rig install --dry-run --show   # what will change, including file contents
rig install                    # asks for confirmation, then for your sudo password
rig doctor                     # check that everything works
```

Only `rig install`, `rig uninstall` and `rig ca rotate` use sudo. `rig install`:

1. Adds an nftables rule that redirects loopback ports 80 → 8080 and 443 → 8443, so nginx
   can run as your user. It affects only traffic to `127.0.0.1` / `::1` and doesn't touch
   your firewall.
2. Adds rig's CA to the system trust store, so browsers, curl and PHP trust site
   certificates.
3. Creates a limited MariaDB account, `rigadmin`, for rig.

## Quick start

```sh
cd ~/code
laravel new my-app
cd my-app
rig link        # serves https://my-app.localhost, offers to set up the database and .env
php artisan migrate
rig open
```

An existing project works the same way: clone it into `~/code/<name>`, set up `.env`, then
`rig link`. Use `rig link --no-db` for a project without a database.

## Commands

Run `rig help` for the list. Where a site name is optional, rig uses the project folder you
are in.

| Command | Does |
|---|---|
| `rig link [name] [--no-db]` | Serve the current project, then offer the database and `.env` step |
| `rig unlink [name]` | Stop serving a site. Code and database are kept. |
| `rig rm <site> [--purge]` | `unlink`, back up and drop the database. `--purge` also deletes the folder, after you type the site name. |
| `rig sites` / `rig open [name]` | List sites / open one in the browser |
| `rig env [name] [--no-db]` | Rewrite the `.env` keys rig manages (diff first, secrets hidden) |
| `rig db list` / `dump <site>` / `drop <site>` | Site databases; `drop` always backs up first |
| `rig worker start\|stop\|restart\|logs <site> <kind>` | `queue`, `schedule` or `reverb` workers (also `list`, `stop --all`, `remove <site> reverb`) |
| `rig mail start\|stop\|status\|open` | Mailpit: SMTP on `127.0.0.1:1025`, inbox at `http://localhost:8025` |
| `rig s3 start\|stop\|status` | Garage S3 storage |
| `rig s3 enable <site>` | Give the site its own bucket and key, and point `.env` at it |
| `rig s3 ls\|get\|put\|rm <site> ...` | Browse and manage the site's files |
| `rig status [--no-db]` | Overview of the stack and sites (`--no-db` skips the MariaDB login) |
| `rig logs <target>` | Follow a log: `nginx`, `php`, `mail`, `s3`, `<site>`, `<site>:queue` ... |
| `rig doctor [--fix] [--security]` | Health, drift and security checks |
| `rig ca rotate` | Replace the CA and reissue every site certificate |
| `rig install` / `rig uninstall` | One-time system setup / undo it (`--dry-run` to preview) |

Notes:

- Databases are named `rig_<site>` (hyphens become underscores), with a user of the same
  name. GUI clients like TablePlus can connect with the values in `.env`.
- Workers never start at login, and logging out stops them. Reverb is bound to `127.0.0.1`
  and routed through `wss://<site>.localhost/app/…` and `/apps/…`; it needs
  `laravel/reverb` in `composer.json`.
- All mail your sites send is caught by Mailpit; nothing leaves the machine.
- Each site's bucket is served at `https://files.<site>.localhost`, so
  `Storage::temporaryUrl()` works in the browser.
- `--json` works on `status`, `doctor`, `sites`, `link`, `unlink`, `db list`, `worker` and
  `s3 ls`, and on `env` and `rm` together with `--yes`, so a script never skips a
  confirmation by accident.

## Dashboard

Run `rig` with no arguments. It shows the health of the stack and every site.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Select a site |
| `o`, `Enter` | Open the site |
| `1` `2` `3` | Toggle its `queue` / `schedule` / `reverb` worker |
| `m` / `s` | Start or stop Mailpit / Garage |
| `r` / `q` | Refresh / quit |

## Omarchy bar widget

On [Omarchy](https://omarchy.org) 4, the `omarchy/` folder is a bar widget: an icon that
is dim when the stack is stopped and red when something needs attention. Click it for a
popup to start or stop Mailpit and Garage, toggle each site's `queue` / `schedule` /
`reverb` worker (`Q` `S` `R`), and open sites. Right-click opens the dashboard.

```sh
install -d ~/.config/omarchy/plugins/vojislavd.rig
install -m 0644 omarchy/* ~/.config/omarchy/plugins/vojislavd.rig/
omarchy-shell shell rescanPlugins
omarchy plugin enable vojislavd.rig --section right
```

To update, copy the files again and run `omarchy-restart-shell` (the automatic reload can
keep old copies of `Service.qml` and `Model.js`).

It runs `~/.local/bin/rig status --json --no-db` every 10 seconds and calls `rig` directly
(no shell, no sudo). Commands that delete data or need root stay in the terminal.

## Security

**What rig protects:** your code and `.env` secrets, local databases, your browser's trust
store, and your user account and root.

| Threat | What rig does |
|---|---|
| A malicious dependency running as you (npm `postinstall`, composer plugin) | It already has your privileges; nothing rig runs or writes gives it a way to root. |
| Someone on the same network | Everything listens on `127.0.0.1` or a unix socket. `rig doctor --security` fails if a rig port is reachable from outside. |
| A website you visit reaching local services (DNS rebinding) | nginx answers only your sites' names; Mailpit's inbox answers only `localhost`; Garage and Reverb need their secrets. |
| Stolen CA key | The CA is name-constrained to `.localhost`, excludes all IP addresses and can't sign sub-CAs. |
| Hostile input (a folder named `foo; rm -rf ~`) | Strict site-name validation, paths confined to `~/code`, no shell anywhere. |
| Mistakes | Code is deleted only with `--purge` and typing the site name; databases are dumped before being dropped. |

**Out of scope:** an attacker who already has root or physical access, and compromised
Arch packages.

### Reporting a vulnerability

Please don't open a public issue. Use GitHub's
[private vulnerability reporting](https://github.com/vojislavd/rig/security/advisories/new).

## How it works

```
browser ──► foo.localhost ──► 127.0.0.1          (no DNS setup needed)
browser ──► 127.0.0.1:443 ──nft redirect──► 127.0.0.1:8443   nginx (user unit)
                                                  ├─► unix socket ─► php-fpm 8.5 (user unit)
                                                  ├─► /app, /apps ─► Reverb (per site, on demand)
                                                  └─► files.foo.localhost ─► Garage, that site's bucket only
MariaDB:  system service, 127.0.0.1:3306
```

## Files

rig only touches files it created. `rig doctor` reports changes to them and
`rig doctor --fix` restores them.

```
~/.config/rig/                 # config, secrets (0600), nginx and PHP config
~/.config/systemd/user/rig-*.service
~/.local/share/rig/            # CA, site certificates, database backups, service data
~/.local/state/rig/logs/       # nginx and PHP logs
$XDG_RUNTIME_DIR/rig/          # sockets
```

System files, created by `rig install` only:

```
/etc/rig/nat.nft                                       # port redirect rules
/etc/systemd/system/rig-nat.service                    # loads them at boot
/etc/ca-certificates/trust-source/anchors/rig-ca.crt   # rig's CA
```

plus the `rigadmin` MariaDB account and its `rigsys` procedures.

## Uninstall

```sh
rig rm <site>            # for each site (backs up and drops its database)
rig uninstall --dry-run  # preview
rig uninstall            # removes the port redirect, CA trust and MariaDB account
rm ~/.local/bin/rig
```

Then delete the folders under [Files](#files) if you don't need the backups.

## Development

```sh
./dev-install.sh
```

Runs `gofmt`, `go vet`, `go test` and
[`govulncheck`](https://go.dev/doc/tutorial/govulncheck), then builds, installs to
`~/.local/bin/rig` and runs `rig doctor`. It stops at the first failure.

## Roadmap

- Desktop notifications
- `rig share`: expose chosen paths (e.g. a webhook) through a time-limited tunnel
- Multiple PHP versions per site
- Git worktree subdomains (`branch.foo.localhost`)
- Valkey, PostgreSQL and Meilisearch

## License

[MIT](LICENSE)
