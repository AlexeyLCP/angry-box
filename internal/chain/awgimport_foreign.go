package chain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/ports"
)

// discoverForeignAWG scans Docker Amnezia containers and toolza3 (/etc/awg3)
// over SSH. When host awg0.conf was empty, the first candidate fills
// ServerConfig + Peers so takeover can materialize them.
func discoverForeignAWG(client ports.SSHClient, useSudo bool, res *ImportResult) {
	ctx := context.Background()
	priv := func(cmd string) string {
		if useSudo {
			return "sudo " + cmd
		}
		return cmd
	}

	if listing, _, _, err := client.RunWithOutput(ctx, priv("ls /etc/awg3/*.conf 2>/dev/null"), 20*time.Second); err == nil {
		for _, path := range strings.Split(listing, "\n") {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			out, _, _, err := client.RunWithOutput(ctx, priv("cat "+path), 20*time.Second)
			if err != nil || strings.TrimSpace(out) == "" {
				continue
			}
			if adoptForeign(res, out, "toolza3", "systemd:awg3") {
				res.Log += "toolza3 " + path + " imported\n"
			}
		}
	}

	namesOut, _, _, err := client.RunWithOutput(ctx, priv("docker ps --format '{{.Names}}' 2>/dev/null"), 20*time.Second)
	if err != nil || strings.TrimSpace(namesOut) == "" {
		return
	}
	for _, name := range strings.Split(namesOut, "\n") {
		name = strings.TrimSpace(name)
		dirs, ok := dockerAWGDirs(name)
		if !ok {
			continue
		}
		for _, dir := range dirs {
			for _, confName := range []string{"awg0.conf", "wg0.conf", "awg.conf"} {
				path := dir + "/" + confName
				out, _, _, err := client.RunWithOutput(ctx, priv("docker exec "+name+" cat "+path+" 2>/dev/null"), 20*time.Second)
				if err != nil || strings.TrimSpace(out) == "" {
					continue
				}
				if adoptForeign(res, out, "docker:"+name, "docker:"+name) {
					res.Log += fmt.Sprintf("docker %s:%s imported\n", name, path)
				}
				break
			}
		}
	}
}

func dockerAWGDirs(name string) ([]string, bool) {
	switch {
	case name == "amnezia-wireguard" || strings.HasPrefix(name, "amnezia-wireguard-"):
		return []string{"/opt/amnezia/wireguard"}, true
	case name == "amnezia-awg3" || strings.HasPrefix(name, "amnezia-awg3-"):
		return []string{"/opt/amnezia/awg3", "/opt/amnezia/awg"}, true
	case name == "amnezia-awg2" || strings.HasPrefix(name, "amnezia-awg2-"):
		return []string{"/opt/amnezia/awg2", "/opt/amnezia/awg"}, true
	case name == "amnezia-awg" || strings.HasPrefix(name, "amnezia-awg-"):
		return []string{"/opt/amnezia/awg"}, true
	default:
		return nil, false
	}
}

func adoptForeign(res *ImportResult, confText, source, stopTarget string) bool {
	sc, ok := parseAWGServerConf(confText)
	if !ok {
		return false
	}
	res.ForeignSources = append(res.ForeignSources, source)
	if stopTarget != "" {
		res.StopTargets = append(res.StopTargets, stopTarget)
	}
	if res.ServerConfig == nil {
		res.ServerConfig = sc
		res.Imported["awg0_conf"] = true
	}
	if len(res.Peers) == 0 {
		if peers := parseAllPeers(confText); len(peers) > 0 {
			res.Peers = peers
			res.Imported["peers"] = true
		}
	}
	return true
}

// StopImportSources stops Docker containers / systemd units that held the
// imported interface so the kernel iface can bind the port.
func StopImportSources(client ports.SSHClient, useSudo bool, targets []string) {
	if client == nil {
		return
	}
	ctx := context.Background()
	priv := func(cmd string) string {
		if useSudo {
			return "sudo " + cmd
		}
		return cmd
	}
	for _, t := range targets {
		kind, name, ok := strings.Cut(t, ":")
		if !ok || name == "" {
			continue
		}
		switch kind {
		case "docker":
			_, _, _, _ = client.RunWithOutput(ctx, priv("docker update --restart=no "+name+" 2>/dev/null; docker stop "+name), 30*time.Second)
		case "systemd":
			_, _, _, _ = client.RunWithOutput(ctx, priv("systemctl stop "+name+" 2>/dev/null"), 20*time.Second)
		}
	}
}
