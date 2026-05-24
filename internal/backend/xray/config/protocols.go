package config

import (
	"encoding/json"
	"fmt"
)

// Protocol presets for v1.
// All presets use XHTTP transport (recommended for Xray v26+).

// VLESSEntryReality returns an inbound spec for a VLESS+Reality+XHTTP entry node.
// This is the external-facing inbound that clients connect to.
func VLESSEntryReality(tag string, clientID string, realityKey string, realityPub string, port int) json.RawMessage {
	rs := map[string]interface{}{
		"serverName":  "discord.com",
		"serverNames": []string{"discord.com"},
		"privateKey":  realityKey,
		"shortId":     "6ba85179",
		"shortIds":    []string{"6ba85179"},
		"fingerprint": "chrome",
		"dest":        "discord.com:443",
		"show":        true,
	}
	if realityPub != "" {
		rs["publicKey"] = realityPub
	}
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients": []map[string]string{
				{"id": clientID, "flow": "xtls-rprx-vision"},
			},
			"decryption": "none",
		},
		"streamSettings": map[string]interface{}{
			"network":  "xhttp",
			"security": "reality",
			"realitySettings": rs,
			"xhttpSettings": map[string]interface{}{
				"host": "discord.com",
				"path": "/download",
				"mode": "packet-up",
			},
		},
	})
}

// VLESSEntryTLS returns an inbound spec for VLESS+TLS+XHTTP (alternative to Reality).
func VLESSEntryTLS(tag string, clientID string, serverName string, port int) json.RawMessage {
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients":    []map[string]string{{"id": clientID}},
			"decryption": "none",
		},
		"streamSettings": map[string]interface{}{
			"network":  "xhttp",
			"security": "tls",
			"tlsSettings": map[string]interface{}{
				"serverName": serverName,
			},
			"xhttpSettings": map[string]interface{}{
				"host": serverName,
				"path": "/xray",
				"mode": "packet-up",
			},
		},
	})
}

// VLESSHop returns an inbound spec for a VLESS+XHTTP intermediate hop node.
// Hops use NO security (internal traffic between your own servers).
func VLESSHop(tag string, clientID string, port int) json.RawMessage {
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients":    []map[string]string{{"id": clientID}},
			"decryption": "none",
		},
		"streamSettings": map[string]interface{}{
			"network":  "xhttp",
			"security": "none",
			"xhttpSettings": map[string]interface{}{
				"host": "localhost",
				"path": fmt.Sprintf("/hop-%s", tag),
				"mode": "stream-one",
			},
		},
	})
}

// VLESSOutbound returns an outbound spec for routing to the next hop.
func VLESSOutbound(tag string, nextServerAddr string, nextServerPort int, clientID string) json.RawMessage {
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"settings": map[string]interface{}{
			"vnext": []map[string]interface{}{{
				"address": nextServerAddr,
				"port":    nextServerPort,
				"users":   []map[string]string{{"id": clientID, "encryption": "none"}},
			}},
		},
		"streamSettings": map[string]interface{}{
			"network":  "xhttp",
			"security": "none",
			"xhttpSettings": map[string]interface{}{
				"host": "localhost",
				"path": fmt.Sprintf("/hop-%s", tag),
				"mode": "stream-one",
			},
		},
	})
}

// TrojanEntry returns an inbound spec for Trojan+TLS+XHTTP (alternative protocol).
func TrojanEntry(tag string, password string, serverName string, port int) json.RawMessage {
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "trojan",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients": []map[string]string{{"password": password}},
		},
		"streamSettings": map[string]interface{}{
			"network":  "xhttp",
			"security": "tls",
			"tlsSettings": map[string]interface{}{
				"serverName": serverName,
			},
			"xhttpSettings": map[string]interface{}{
				"host": serverName,
				"path": "/trojan",
				"mode": "packet-up",
			},
		},
	})
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
