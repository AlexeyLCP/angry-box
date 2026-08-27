package chain

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/awg/vpnuri"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// ParseAWGOutboundConf fills an AWG outbound from a client .conf or vpn:// URI.
func ParseAWGOutboundConf(raw string) (*model.AWGOutbound, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "vpn://") {
		payload, err := vpnuri.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("vpn://: %w", err)
		}
		conf, err := vpnuri.ConfFromPayload(payload)
		if err != nil {
			return nil, fmt.Errorf("vpn:// conf: %w", err)
		}
		raw = conf
	}
	if !strings.Contains(raw, "[Interface]") || !strings.Contains(raw, "[Peer]") {
		return nil, fmt.Errorf("not an AWG client .conf")
	}
	iface, peer := parseSections(raw)
	ob := &model.AWGOutbound{
		PrivateKey:   iface["PrivateKey"],
		Address:      iface["Address"],
		PublicKey:    peer["PublicKey"],
		PresharedKey: peer["PresharedKey"],
		Endpoint:     peer["Endpoint"],
		AllowedIPs:   peer["AllowedIPs"],
		H1:           iface["H1"], H2: iface["H2"], H3: iface["H3"], H4: iface["H4"],
		HeaderProtectionKey: iface["HeaderProtectionKey"],
		Enabled:             true,
	}
	ob.MTU, _ = strconv.Atoi(iface["MTU"])
	if ob.MTU == 0 {
		ob.MTU = 1420
	}
	ob.Keepalive, _ = strconv.Atoi(peer["PersistentKeepalive"])
	ob.Jc, _ = strconv.Atoi(iface["Jc"])
	ob.Jmin, _ = strconv.Atoi(iface["Jmin"])
	ob.Jmax, _ = strconv.Atoi(iface["Jmax"])
	ob.S1, _ = strconv.Atoi(iface["S1"])
	ob.S2, _ = strconv.Atoi(iface["S2"])
	ob.S3, _ = strconv.Atoi(iface["S3"])
	ob.S4, _ = strconv.Atoi(iface["S4"])
	if ob.HeaderProtectionKey != "" {
		ob.AWGVersion = model.AWGVersion3
	} else if iface["S3"] != "" || iface["I1"] != "" {
		ob.AWGVersion = model.AWGVersion2
	} else {
		ob.AWGVersion = model.AWGVersion2
	}
	if ob.PrivateKey == "" || ob.PublicKey == "" || ob.Endpoint == "" {
		return nil, fmt.Errorf("conf missing PrivateKey, PublicKey or Endpoint")
	}
	if ob.Address == "" {
		ob.Address = "10.9.0.2/32"
	}
	if ob.AllowedIPs == "" {
		ob.AllowedIPs = "0.0.0.0/0, ::/0"
	}
	return ob, nil
}

// RenderAWGOutboundConf builds awg-quick client conf (Table=off, no DNS, no I1-I5).
func RenderAWGOutboundConf(ob model.AWGOutbound) string {
	if ob.MTU == 0 {
		ob.MTU = 1420
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", ob.PrivateKey)
	fmt.Fprintf(&b, "Address = %s\n", ob.Address)
	fmt.Fprintf(&b, "MTU = %d\n", ob.MTU)
	b.WriteString("Table = off\n")
	if ob.Jc > 0 {
		fmt.Fprintf(&b, "Jc = %d\n", ob.Jc)
		fmt.Fprintf(&b, "Jmin = %d\n", ob.Jmin)
		fmt.Fprintf(&b, "Jmax = %d\n", ob.Jmax)
		fmt.Fprintf(&b, "S1 = %d\n", ob.S1)
		fmt.Fprintf(&b, "S2 = %d\n", ob.S2)
		if model.AWGVersionAtLeast(ob.AWGVersion, model.AWGVersion2) {
			fmt.Fprintf(&b, "S3 = %d\n", ob.S3)
			fmt.Fprintf(&b, "S4 = %d\n", ob.S4)
		}
		if ob.H1 != "" {
			fmt.Fprintf(&b, "H1 = %s\n", ob.H1)
			fmt.Fprintf(&b, "H2 = %s\n", ob.H2)
			fmt.Fprintf(&b, "H3 = %s\n", ob.H3)
			fmt.Fprintf(&b, "H4 = %s\n", ob.H4)
		}
		if model.IsAWG3Family(ob.AWGVersion) && ob.HeaderProtectionKey != "" {
			fmt.Fprintf(&b, "HeaderProtectionKey = %s\n", ob.HeaderProtectionKey)
		}
	}
	fmt.Fprintf(&b, "PostUp = sysctl -w net.ipv4.conf.%s.rp_filter=0 2>/dev/null || true\n", ob.IfaceName())
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", ob.PublicKey)
	if ob.PresharedKey != "" {
		fmt.Fprintf(&b, "PresharedKey = %s\n", ob.PresharedKey)
	}
	fmt.Fprintf(&b, "AllowedIPs = %s\n", ob.AllowedIPs)
	fmt.Fprintf(&b, "Endpoint = %s\n", ob.Endpoint)
	ka := ob.Keepalive
	if ka == 0 {
		ka = 25
	}
	fmt.Fprintf(&b, "PersistentKeepalive = %d\n", ka)
	return b.String()
}

func injectAWGOutboundDirects(outbounds *[]json.RawMessage, seen map[string]bool, nodeInfo *model.NodeInfo) {
	if nodeInfo == nil {
		return
	}
	for i, ob := range nodeInfo.AWGOutbounds {
		if !ob.Enabled || ob.Endpoint == "" {
			continue
		}
		tag := ob.Tag
		if tag == "" {
			tag = fmt.Sprintf("awgo-%d", i+1)
		}
		if seen[tag] {
			continue
		}
		data, _ := json.Marshal(config.DirectOutbound{
			Type:          "direct",
			Tag:           tag,
			BindInterface: ob.IfaceName(),
		})
		seen[tag] = true
		*outbounds = append(*outbounds, data)
	}
}
