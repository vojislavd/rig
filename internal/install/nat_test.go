package install

import (
	"slices"
	"strings"
	"testing"
)

func TestNatRules(t *testing.T) {
	rules := string(natRules())

	for _, want := range []string{
		"table inet rig_nat {",
		"type nat hook output priority dstnat; policy accept;",
		"ip  daddr 127.0.0.1 tcp dport 80  redirect to :8080",
		"ip  daddr 127.0.0.1 tcp dport 443 redirect to :8443",
		"ip6 daddr ::1       tcp dport 80  redirect to :8080",
		"ip6 daddr ::1       tcp dport 443 redirect to :8443",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules missing %q", want)
		}
	}

	// Every redirect must be limited to loopback, so no traffic to a real
	// site can ever match.
	for _, line := range strings.Split(rules, "\n") {
		if strings.Contains(line, "redirect") &&
			!strings.Contains(line, "daddr 127.0.0.1 ") && !strings.Contains(line, "daddr ::1 ") {
			t.Errorf("redirect not limited to loopback: %q", strings.TrimSpace(line))
		}
	}

	// Only the output hook (this machine's own connections), and the
	// rules never accept or drop traffic themselves.
	for _, banned := range []string{"hook prerouting", "hook input", "hook forward", "drop", "masquerade", "dnat"} {
		if strings.Contains(rules, banned) {
			t.Errorf("rules contain %q", banned)
		}
	}
	if n := strings.Count(rules, "redirect"); n != 4 {
		t.Errorf("found %d redirects, want 4", n)
	}
}

func TestNatUnit(t *testing.T) {
	unit := string(natUnit())
	for _, want := range []string{
		"ExecStartPre=/usr/bin/nft --check --file /etc/rig/nat.nft\n",
		"ExecStart=/usr/bin/nft --file /etc/rig/nat.nft\n",
		"ExecStop=/usr/bin/nft delete table inet rig_nat\n",
		"CapabilityBoundingSet=CAP_NET_ADMIN\n",
		"RestrictAddressFamilies=AF_NETLINK\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q", want)
		}
	}
}

func TestBuildOrder(t *testing.T) {
	p := Build(testInputs(t))
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}

	// All writes come before any command.
	seenRun := false
	for i, a := range p.Actions {
		if a.Kind == Run {
			seenRun = true
		} else if seenRun {
			t.Errorf("action %d writes a file after a command ran", i+1)
		}
	}

	n := len(p.Actions)
	if n < 2 {
		t.Fatalf("plan has %d actions, want at least 2", n)
	}
	if got := p.Actions[n-2].Argv; !slices.Equal(got, []string{"/usr/bin/systemctl", "daemon-reload"}) {
		t.Errorf("second to last action = %v, want daemon-reload", got)
	}
	if got := p.Actions[n-1].Argv; !slices.Contains(got, NatUnitName) || !slices.Contains(got, "--now") {
		t.Errorf("last action = %v, want enable --now ... %s", got, NatUnitName)
	}
}
