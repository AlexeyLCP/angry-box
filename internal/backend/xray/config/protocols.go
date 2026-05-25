package config

import (
	"encoding/json"
	"fmt"
)

// Protocol presets for v1.
// All presets support parameterized transport (XHTTP is default/recommended for v26+).

// VLESSEntryReality returns an inbound spec for a VLESS+Reality entry node.
func VLESSEntryReality(tag string, clientID string, realityKey string, realityPub string, port int, transport string, host string, path string, mode string, fingerprint string) json.RawMessage {
	rs := map[string]interface{}{
		"serverName":  "discord.com",
		"serverNames": []string{"discord.com"},
		"privateKey":  realityKey,
		"shortId":     "6ba85179",
		"shortIds":    []string{"6ba85179"},
		"fingerprint": fingerprint,
		"dest":        "discord.com:443",
		"show":        true,
	}
	if realityPub != "" {
		rs["publicKey"] = realityPub
	}
	if host == "" {
		host = "discord.com"
	}
	if path == "" {
		path = "/download"
	}
	if mode == "" {
		mode = "packet-up"
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
		"streamSettings": buildStreamSettings(transport, "reality", rs, host, path, mode, fingerprint),
	})
}

// VLESSEntryTLS returns an inbound spec for VLESS+TLS entry node.
func VLESSEntryTLS(tag string, clientID string, serverName string, port int, transport string, host string, path string, mode string, fingerprint string) json.RawMessage {
	if host == "" {
		host = serverName
	}
	if path == "" {
		path = "/xray"
	}
	if mode == "" {
		mode = "packet-up"
	}
	tls := map[string]interface{}{
		"serverName":  serverName,
		"fingerprint": fingerprint,
	}
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients":    []map[string]string{{"id": clientID}},
			"decryption": "none",
		},
		"streamSettings": buildStreamSettings(transport, "tls", tls, host, path, mode, fingerprint),
	})
}

// VLESSHop returns an inbound spec for a VLESS intermediate hop node.
func VLESSHop(tag string, clientID string, port int, transport string, mode string) json.RawMessage {
	if mode == "" {
		mode = "stream-one"
	}
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "vless",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients":    []map[string]string{{"id": clientID}},
			"decryption": "none",
		},
		"streamSettings": buildStreamSettings(transport, "none", nil, "localhost", fmt.Sprintf("/hop-%s", tag), mode, ""),
	})
}

// VLESSOutbound returns an outbound spec for routing to the next hop.
func VLESSOutbound(tag string, nextServerAddr string, nextServerPort int, clientID string, transport string, mode string) json.RawMessage {
	if mode == "" {
		mode = "stream-one"
	}
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
		"streamSettings": buildStreamSettings(transport, "none", nil, "localhost", fmt.Sprintf("/hop-%s", tag), mode, ""),
	})
}

// TrojanEntry returns an inbound spec for Trojan+TLS entry node.
func TrojanEntry(tag string, password string, serverName string, port int, transport string, host string, path string, mode string, fingerprint string) json.RawMessage {
	if host == "" {
		host = serverName
	}
	if path == "" {
		path = "/trojan"
	}
	if mode == "" {
		mode = "packet-up"
	}
	tls := map[string]interface{}{
		"serverName":  serverName,
		"fingerprint": fingerprint,
	}
	return mustJSON(map[string]interface{}{
		"tag":      tag,
		"protocol": "trojan",
		"port":     port,
		"listen":   "0.0.0.0",
		"settings": map[string]interface{}{
			"clients": []map[string]string{{"password": password}},
		},
		"streamSettings": buildStreamSettings(transport, "tls", tls, host, path, mode, fingerprint),
	})
}

// buildStreamSettings constructs a transport-agnostic streamSettings block.
func buildStreamSettings(transport string, security string, securitySettings interface{}, host string, path string, mode string, fingerprint string) map[string]interface{} {
	ss := map[string]interface{}{
		"network":  transport,
		"security": security,
	}

	switch security {
	case "reality":
		ss["realitySettings"] = securitySettings
	case "tls":
		ss["tlsSettings"] = securitySettings
	}

	switch transport {
	case "xhttp":
		xhttp := map[string]interface{}{"mode": mode}
		if host != "" {
			xhttp["host"] = host
		}
		if path != "" {
			xhttp["path"] = path
		}
		ss["xhttpSettings"] = xhttp
	case "ws":
		ws := map[string]interface{}{}
		if host != "" {
			ws["host"] = host
		}
		if path != "" {
			ws["path"] = path
		}
		ss["wsSettings"] = ws
	case "grpc":
		ss["grpcSettings"] = map[string]interface{}{
			"serviceName": path,
			"multiMode":   mode == "packet-up",
		}
	case "tcp":
		ss["tcpSettings"] = map[string]interface{}{
			"header": map[string]interface{}{"type": "none"},
		}
	}

	return ss
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
