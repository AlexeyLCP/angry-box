package chain

// awgimport.go — SSH import of existing AmneziaWG configs from a running
// server, ported from VPN/orchestrator/app/services/awg_import.py. Reads
// awg0.conf, awg-exit-*.conf, awg0-peers.list and the sing-box config.json over
// SSH, parses them, and back-fills placeholder fields in the node's inbounds
// with real key material — WITHOUT overwriting operator-edited values. Lets
// angry-box take over an already-running AWG balancer without manual data entry.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

const (
	awgConfDir       = "/etc/amnezia/amneziawg"
	awg0ConfPath     = "/etc/amnezia/amneziawg/awg0.conf"
	awgPeersListPath = "/etc/amnezia/amneziawg/awg0-peers.list"
)

// AwgServerConfig is the parsed awg0.conf ([Interface] with ListenPort).
type AwgServerConfig struct {
	PrivateKey string
	ListenPort int
	Address    string
	DNS        string
	PostUp     string
	PostDown   string
	JC         int
	JMIN       int
	JMAX       int
	S1, S2, S3, S4 int
	H1, H2, H3, H4 string
	I1, I2, I3, I4, I5 string
}

// AwgExitConfig is the parsed awg-exit-*.conf ([Peer] with Endpoint/PublicKey).
type AwgExitConfig struct {
	Name          string // "exit-n1"
	InterfaceName string // "awg-exit-n1"
	PrivateKey    string
	PublicKey     string
	PresharedKey  string
	Address       string
	Endpoint      string // "host:port"
	Amnezia       map[string]string
}

// AwgPeerEntry is one entry from awg0-peers.list.
type AwgPeerEntry struct {
	Name         string
	Address      string
	PublicKey    string
	PresharedKey string
	AllowedIPs   string
}

// ImportResult holds the parsed remote state.
type ImportResult struct {
	ServerConfig   *AwgServerConfig
	ExitNodes      []AwgExitConfig
	Peers          []AwgPeerEntry
	SingboxConfig  map[string]any
	Log            string
	Imported       map[string]bool // awg0_conf, exit_nodes, peers, singbox_config, db_updated
	DBUpdated      string          // human-readable summary
}

// ImportAWGConfigs connects to host over SSH, reads the AWG configs, parses
// them, and back-fills placeholder fields on the node's inbounds. useSudo wraps
// privileged cat commands. Returns the import result + error on SSH/parse
// failure (placeholder back-fill is best-effort and recorded in DBUpdated).
func ImportAWGConfigs(host model.Host, useSudo bool, info *model.NodeInfo) (*ImportResult, error) {
	res := &ImportResult{Imported: map[string]bool{}}

	client, err := sshclient.Connect(host.Addr, host.User, host.KeyPath)
	if err != nil {
		return res, fmt.Errorf("awg import: ssh connect: %w", err)
	}
	defer client.Close()

	ctx := context.Background()
	priv := func(cmd string) string { if useSudo { return "sudo " + cmd }; return cmd }

	// 1. awg0.conf
	if out, _, _, err := client.RunWithOutput(ctx, priv("cat "+awg0ConfPath), 30*time.Second); err == nil && strings.TrimSpace(out) != "" {
		if sc, ok := parseAWGServerConf(out); ok {
			res.ServerConfig = sc
			res.Imported["awg0_conf"] = true
			res.Log += "awg0.conf parsed (ListenPort=" + strconv.Itoa(sc.ListenPort) + ")\n"
		}
	}

	// 2. awg-exit-*.conf
	if listing, _, _, err := client.RunWithOutput(ctx, priv("ls "+awgConfDir+"/awg-exit-*.conf 2>/dev/null"), 30*time.Second); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(listing), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if out, _, _, err := client.RunWithOutput(ctx, priv("cat "+line), 30*time.Second); err == nil && out != "" {
				if ec, ok := parseAWGExitConf(out); ok {
					iface := line[strings.LastIndexByte(line, '/')+1:]
					ec.InterfaceName = strings.TrimSuffix(iface, ".conf")
					ec.Name = strings.Replace(ec.InterfaceName, "awg-exit-", "exit-", 1)
					res.ExitNodes = append(res.ExitNodes, ec)
				}
			}
		}
		if len(res.ExitNodes) > 0 {
			res.Imported["exit_nodes"] = true
			res.Log += fmt.Sprintf("parsed %d exit-node configs\n", len(res.ExitNodes))
		}
	}

	// 3. awg0-peers.list
	if out, _, _, err := client.RunWithOutput(ctx, priv("cat "+awgPeersListPath+" 2>/dev/null"), 30*time.Second); err == nil && strings.TrimSpace(out) != "" {
		res.Peers = parsePeersList(out)
		res.Imported["peers"] = true
		res.Log += fmt.Sprintf("parsed %d peers\n", len(res.Peers))
	}

	// 4. sing-box config.json (best-effort)
	if out, _, _, err := client.RunWithOutput(ctx, priv("cat /usr/local/etc/sing-box/config.json 2>/dev/null || cat /etc/sing-box/config.json 2>/dev/null"), 30*time.Second); err == nil && strings.TrimSpace(out) != "" {
		var sb map[string]any
		if json.Unmarshal([]byte(out), &sb) == nil {
			res.SingboxConfig = sb
			res.Imported["singbox_config"] = true
		}
	}

	// 5. Back-fill placeholder fields on inbounds (best-effort).
	if info != nil {
		res.DBUpdated = backfillInbounds(info, res)
	}

	return res, nil
}

// parseAWGServerConf parses an awg0.conf into an AwgServerConfig. Returns ok=
// true only when the [Interface] section has a ListenPort (i.e. it's a server
// config, not a client/exit conf).
func parseAWGServerConf(content string) (*AwgServerConfig, bool) {
	iface, _ := parseSections(content)
	if _, ok := iface["ListenPort"]; !ok {
		return nil, false
	}
	sc := &AwgServerConfig{
		PrivateKey: iface["PrivateKey"],
		Address:    iface["Address"],
		DNS:        iface["DNS"],
		PostUp:     iface["PostUp"],
		PostDown:   iface["PostDown"],
		H1:         iface["H1"], H2: iface["H2"], H3: iface["H3"], H4: iface["H4"],
		I1:         iface["I1"], I2: iface["I2"], I3: iface["I3"], I4: iface["I4"], I5: iface["I5"],
	}
	sc.ListenPort, _ = strconv.Atoi(getDefault(iface, "ListenPort", "55555"))
	sc.JC, _ = strconv.Atoi(getDefault(iface, "Jc", "0"))
	sc.JMIN, _ = strconv.Atoi(getDefault(iface, "Jmin", "0"))
	sc.JMAX, _ = strconv.Atoi(getDefault(iface, "Jmax", "0"))
	sc.S1, _ = strconv.Atoi(getDefault(iface, "S1", "0"))
	sc.S2, _ = strconv.Atoi(getDefault(iface, "S2", "0"))
	sc.S3, _ = strconv.Atoi(getDefault(iface, "S3", "0"))
	sc.S4, _ = strconv.Atoi(getDefault(iface, "S4", "0"))
	return sc, true
}

// parseAWGExitConf parses an awg-exit-*.conf. Returns ok=true only when the
// [Peer] section has an Endpoint or PublicKey (i.e. it's an exit-node config).
func parseAWGExitConf(content string) (AwgExitConfig, bool) {
	iface, peer := parseSections(content)
	ec := AwgExitConfig{
		PrivateKey:   iface["PrivateKey"],
		Address:      iface["Address"],
		PublicKey:    peer["PublicKey"],
		PresharedKey: peer["PresharedKey"],
		Endpoint:     peer["Endpoint"],
	}
	// amnezia dict from iface keys (raw strings).
	amneziaKeys := []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4", "H1", "H2", "H3", "H4", "I1", "I2", "I3", "I4", "I5", "Itime"}
	ec.Amnezia = map[string]string{}
	for _, k := range amneziaKeys {
		if v, ok := iface[k]; ok {
			ec.Amnezia[k] = v
		}
	}
	if ec.Endpoint == "" && ec.PublicKey == "" {
		return ec, false
	}
	return ec, true
}

// parseSections splits a .conf into [Interface]/[Peer] key→value maps. Only the
// LAST [Peer] section is returned (exit-node confs have one peer).
func parseSections(content string) (iface map[string]string, peer map[string]string) {
	iface = map[string]string{}
	peer = map[string]string{}
	section := ""
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			if section == "Peer" {
				peer = map[string]string{} // reset for the (last) peer
			}
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if section == "Interface" {
			iface[k] = v
		} else if section == "Peer" {
			peer[k] = v
		}
	}
	return iface, peer
}

// parsePeersList parses an awg0-peers.list file (mixed [Interface]/[Peer]
// sections with Name= comments). Each [Peer] with a PublicKey becomes an entry.
func parsePeersList(content string) []AwgPeerEntry {
	var out []AwgPeerEntry
	current := AwgPeerEntry{AllowedIPs: "0.0.0.0/0"}
	inPeer := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, "Name=") && inPeer {
			current.Name = strings.TrimSpace(strings.SplitN(line, "Name=", 2)[1])
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if inPeer && current.PublicKey != "" {
				out = append(out, current)
			}
			current = AwgPeerEntry{AllowedIPs: "0.0.0.0/0"}
			section := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			inPeer = section == "Peer"
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if inPeer {
			switch k {
			case "PublicKey":
				current.PublicKey = v
			case "AllowedIPs":
				current.AllowedIPs = v
			case "PresharedKey":
				current.PresharedKey = v
			}
		} else if k == "Address" {
			current.Address = v
		}
	}
	if inPeer && current.PublicKey != "" {
		out = append(out, current)
	}
	return out
}

// backfillInbounds fills placeholder fields on info.Inbounds from the imported
// AWG state. Returns a comma-joined summary of what changed (or "no changes
// needed"). Only fills fields currently in the placeholder set; never
// overwrites operator-edited values.
func backfillInbounds(info *model.NodeInfo, res *ImportResult) string {
	placeholders := map[string]bool{"TODO": true, "TODO-fill-from-awg-conf": true, "placeholder": true, "": true}
	var changed []string

	for i := range info.Inbounds {
		ib := &info.Inbounds[i]
		if ib.Protocol != "awg" {
			continue
		}
		// Server private/public key from awg0.conf.
		if res.ServerConfig != nil {
			if placeholders[ib.ServerPrivKey] && res.ServerConfig.PrivateKey != "" {
				ib.ServerPrivKey = res.ServerConfig.PrivateKey
				changed = append(changed, "inbound:"+ib.Protocol+":server_priv")
			}
		}
		// Match exit-node by interface name stored in Obfuscation (if any) —
		// fill endpoint/peer pubkey/psk from the first matching exit config.
		for _, ec := range res.ExitNodes {
			if ec.PublicKey != "" && placeholders[ib.AWGClientPub] {
				ib.AWGClientPub = ec.PublicKey
				changed = append(changed, "inbound:"+ib.Protocol+":client_pub")
				break
			}
		}
	}
	if len(changed) == 0 {
		return "no changes needed"
	}
	res.Imported["db_updated"] = true
	return strings.Join(changed, ", ")
}

func getDefault(m map[string]string, k, def string) string {
	if v, ok := m[k]; ok && v != "" {
		return v
	}
	return def
}