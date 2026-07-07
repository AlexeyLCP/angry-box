package chain

// store_bench_test.go — benchmarks for the store hot paths (CTO-review §13).
// NOT run by CI (no -bench flag in the test step); invoke explicitly:
//   go test -bench=. -benchmem -run=^$ ./internal/chain/

import (
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// BenchmarkSaveAuditLog measures one jsonl append (the v0.3.1 write-amplification
// fix: O(1) append vs the old full-store rewrite).
func BenchmarkSaveAuditLog(b *testing.B) {
	s := NewStore(b.TempDir() + "/store.json")
	entry := &model.AuditLog{Action: "bench", TargetType: "test", Actor: "bench"}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		entry.ID = "" // force newID each iteration
		if err := s.SaveAuditLog(entry); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStoreReadStore measures a full-file readStore on a store seeded
// with 50 hosts (a realistic mid-size fleet). This is the O(file) cost every
// SaveX pays (read-modify-write).
func BenchmarkStoreReadStore(b *testing.B) {
	s := NewStore(b.TempDir() + "/store.json")
	for i := 0; i < 50; i++ {
		s.SaveHost(&model.Host{ID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Addr: "10.0.0.1:22"})
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.mu.RLock()
		_, err := s.readStore()
		s.mu.RUnlock()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStoreWriteStore measures a full-file writeStore on a 50-host store.
func BenchmarkStoreWriteStore(b *testing.B) {
	s := NewStore(b.TempDir() + "/store.json")
	for i := 0; i < 50; i++ {
		s.SaveHost(&model.Host{ID: string(rune('a'+i%26)) + string(rune('0'+i/26)), Addr: "10.0.0.1:22"})
	}
	s.mu.RLock()
	sf, _ := s.readStore()
	s.mu.RUnlock()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s.mu.Lock()
		if err := s.writeStore(sf); err != nil {
			b.Fatal(err)
		}
		s.mu.Unlock()
	}
}

// BenchmarkGenerateProxyPassword measures the rejection-sampling password
// generator (crypto/rand + big.Int per char — the modulo-bias fix).
func BenchmarkGenerateProxyPassword(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := GenerateProxyPassword()
		if err != nil {
			b.Fatal(err)
		}
	}
}