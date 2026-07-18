// awgtrial — one-off P0a egress-trial driver. Deploys a standalone AWG inbound
// on n2 (144.31.157.106) via ApplyMergedNode with AB_AWG_AUTO_REDIRECT=1, then
// renders an awg-quick client .conf (CPS via PostUp awg set — kernel 6.12
// compatible) for the trial client on n1. Idempotent: re-run reuses the stored
// node/user and skips the redeploy.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

func main() {
	dir := `C:\Temp\ab-trial`
	os.MkdirAll(dir, 0o755)
	st := chain.NewStore(filepath.Join(dir, "store.json"))

	host := &model.Host{ID: "n2", Addr: "144.31.157.106:22", User: "root", KeyPath: `C:\Users\dante\.ssh\id_ed25519`}
	must(st.SaveHost(host))

	// User first — the deploy renders one [Peer] per credentialed user.
	u, _ := st.GetUser("u-trial")
	if u == nil || u.AWGPrivateKey == "" {
		u = &model.User{ID: "u-trial", Name: "trial", Active: true, Protocols: []string{"awg"}}
		must(chain.EnsureUserCreds(u))
		chain.EnsureUserAWGAddress(u, nil)
		must(st.SaveUser(u))
	}
	fmt.Printf("user: AWGAddress=%s pub=%s\n", u.AWGAddress, u.AWGPublicKey)

	ni, _ := st.GetNodeInfo("n2")
	deployed := ni != nil && len(ni.Inbounds) > 0 && ni.Inbounds[0].ServerPubKey != ""
	if !deployed {
		inbound := model.NodeInbound{
			Protocol: "awg",
			Port:     51840,
			Tag:      "trial-awg",
			ForUsers: []string{"u-trial"},
		}
		ni = &model.NodeInfo{
			Host:     *host,
			Inbounds: []model.NodeInbound{inbound},
		}
		must(st.SaveNodeInfo(ni))

		sshclient.SetHostKeyManager(st)
		sshclient.SetKeyResolver(st)

		f := factory.New(nil)
		applier := chain.NewApplier(f, nil)

		report, merge, err := applier.ApplyMergedNode(context.Background(), st, ni)
		if merge != nil {
			for _, w := range merge.Warnings {
				fmt.Printf("merge warning: %s\n", w)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "ApplyMergedNode failed: %v\n", err)
			if report != nil {
				fmt.Fprintf(os.Stderr, "report: %+v\n", report)
			}
			os.Exit(1)
		}
		fmt.Printf("apply report: %+v\n", report)
		// The deploy generated ServerPrivKey/PubKey into the in-memory inbound;
		// persist them (re-apply must reuse, not regenerate).
		must(st.SaveNodeInfo(ni))
	} else {
		fmt.Println("node already deployed — reusing stored server keys")
	}

	ib := &ni.Inbounds[0]
	fmt.Printf("server pub=%s port=%d\n", ib.ServerPubKey, ib.Port)

	// Client conf: amnezia from the default preset (the server renders the same
	// preset; I1-I5 fresh here — the server ignores them, they are client-side
	// decoys).
	preset := chain.GetDefaultPreset()
	amn := chain.BuildAWGAmnezia(preset.AWG, &preset, nil)
	confPath := filepath.Join(dir, "awgc0.conf")
	f2, err := os.Create(confPath)
	must(err)
	defer f2.Close()
	fmt.Fprintf(f2, "[Interface]\nAddress = %s\nPrivateKey = %s\nMTU = 1420\nTable = off\n", u.AWGAddress, u.AWGPrivateKey)
	fmt.Fprintf(f2, "Jc = %d\nJmin = %d\nJmax = %d\nS1 = %d\nS2 = %d\nS3 = %d\nS4 = %d\n",
		amn.JC, amn.JMIN, amn.JMAX, amn.S1, amn.S2, amn.S3, amn.S4)
	fmt.Fprintf(f2, "H1 = %s\nH2 = %s\nH3 = %s\nH4 = %s\n", amn.H1, amn.H2, amn.H3, amn.H4)
	// CPS via PostUp UAPI — awg setconf on kernel 6.12 rejects inline I1-I5.
	fmt.Fprintf(f2, "PostUp = awg set awgc0 i1 %q i2 %q i3 %q i4 %q i5 %q\n", amn.I1, amn.I2, amn.I3, amn.I4, amn.I5)
	fmt.Fprintf(f2, "\n[Peer]\nPublicKey = %s\nAllowedIPs = 0.0.0.0/0, ::/0\nEndpoint = 144.31.157.106:%d\nPersistentKeepalive = 25\n",
		ib.ServerPubKey, ib.Port)
	fmt.Printf("client conf written: %s\n", confPath)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
