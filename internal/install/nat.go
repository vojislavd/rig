package install

import "fmt"

// Port redirect: nginx runs as the user, so it can't
// open ports below 1024. It listens on 8080/8443, and an nftables rule
// sends this machine's own connections to ports 80/443 there.
const (
	httpPort  = 8080
	httpsPort = 8443

	natTable    = "rig_nat"
	natConfPath = "/etc/rig/nat.nft"
	NatUnitName = "rig-nat.service"
	natUnitPath = "/etc/systemd/system/" + NatUnitName
)

// natRules returns the nftables rules.
//
// Security: every rule matches only connections to 127.0.0.1 or ::1
// that start on this machine (the "output" hook), so traffic to any
// real site never matches. The rules live in rig's own table, separate
// from ufw, and only redirect; they never accept or drop anything.
//
// The first two lines make loading repeatable: create the table if it is
// missing, delete it, then define it fresh. nft applies the whole file
// as one transaction, so there is never a moment without the rules.
func natRules() []byte {
	return []byte(header + fmt.Sprintf(`
table inet %[1]s
delete table inet %[1]s

table inet %[1]s {
	chain output {
		type nat hook output priority dstnat; policy accept;

		ip  daddr 127.0.0.1 tcp dport 80  redirect to :%[2]d
		ip  daddr 127.0.0.1 tcp dport 443 redirect to :%[3]d
		ip6 daddr ::1       tcp dport 80  redirect to :%[2]d
		ip6 daddr ::1       tcp dport 443 redirect to :%[3]d
	}
}
`, natTable, httpPort, httpsPort))
}

// natUnit returns the systemd unit that loads natRules at boot.
//
// Security: it runs as root only to load firewall rules, with just the
// CAP_NET_ADMIN capability, netlink sockets and a sandbox.
func natUnit() []byte {
	return []byte(header + fmt.Sprintf(`
[Unit]
Description=rig: send this machine's connections to ports 80/443 to rig's nginx (%[3]d/%[4]d)

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStartPre=/usr/bin/nft --check --file %[2]s
ExecStart=/usr/bin/nft --file %[2]s
ExecStop=/usr/bin/nft delete table inet %[1]s

# Only the right to change network rules; nothing else.
CapabilityBoundingSet=CAP_NET_ADMIN
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictAddressFamilies=AF_NETLINK
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service

[Install]
WantedBy=multi-user.target
`, natTable, natConfPath, httpPort, httpsPort))
}

// natFiles returns the plan steps that write the redirect files.
func natFiles() []Action {
	return []Action{
		{
			Kind: WriteFile, Path: natConfPath, Mode: 0o644, Content: natRules(),
			Why: "rule: this machine's own connections to ports 80/443 go to 8080/8443 (loopback only)",
		},
		{
			Kind: WriteFile, Path: natUnitPath, Mode: 0o644, Content: natUnit(),
			Why: "load that rule at every boot, as a sandboxed service",
		},
	}
}
