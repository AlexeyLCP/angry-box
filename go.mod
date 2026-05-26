module github.com/alexeylcp/lucx-core

go 1.26

require (
	github.com/go-chi/chi/v5 v5.3.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/mattn/go-sqlite3 v1.14.44
	github.com/xtls/xray-core v1.260327.0
	golang.org/x/crypto v0.52.0
	google.golang.org/grpc v1.81.1
	modernc.org/sqlite v1.50.1
)

// Build tag notes:
//   default (no tag)   → modernc.org/sqlite (pure Go, CGO_ENABLED=0)
//   -tags sqlite_cgo   → github.com/mattn/go-sqlite3 (CGO, for MIPS via dockcross)

require (
	github.com/andybalholm/brotli v1.0.6 // indirect
	github.com/apernet/quic-go v0.59.1-0.20260217092621-db4786c77a22 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/compress v1.17.4 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pires/go-proxyproto v0.11.0 // indirect
	github.com/refraction-networking/utls v1.8.3-0.20260301010127-aa6edf4b11af // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sagernet/sing v0.5.1 // indirect
	github.com/xtls/reality v0.0.0-20260322125925-9234c772ba8f // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/mod v0.35.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	golang.org/x/tools v0.44.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	lukechampine.com/blake3 v1.4.1 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
