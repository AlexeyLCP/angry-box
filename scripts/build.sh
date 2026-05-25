#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUILD_DIR="$ROOT/build"
WEB_DIR="$ROOT/lucx-web"
EMBED_DIR="$ROOT/web/dist"
APP="lucx-core"
VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo 'dev')}"
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LDFLAGS="-s -w -X github.com/alexeylcp/lucx-core/internal/api.Version=${VERSION}"

echo "=== LucX CI/CD Build ==="
echo "Version: $VERSION"
echo "Root:    $ROOT"

# ── 1. Go tests ──
echo ""
echo "── 1/4 Go tests ──"
cd "$ROOT"
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./... -count=1 -timeout 60s
echo "   ✓ tests passed"

# ── 2. Web UI type-check + build ──
echo ""
echo "── 2/4 Web UI build ──"
cd "$WEB_DIR"
npm ci --silent
npx vue-tsc --noEmit
npm run build
echo "   ✓ web built"

# ── 3. Copy dist for Go embed ──
echo ""
echo "── 3/4 Embed dist ──"
cd "$ROOT"
rm -rf "$EMBED_DIR"
cp -r "$WEB_DIR/dist" "$EMBED_DIR"
echo "   ✓ dist copied to web/dist"

# ── 4. Build Go binary ──
echo ""
echo "── 4/4 Go build ($VERSION) ──"
mkdir -p "$BUILD_DIR"
CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o "$BUILD_DIR/$APP" ./cmd/$APP/
echo "   ✓ binary: $BUILD_DIR/$APP"

# ── Size ──
echo ""
echo "=== Build Complete ==="
ls -lh "$BUILD_DIR/$APP"
echo ""
echo "Run: $BUILD_DIR/$APP -listen :80 -db ./lucx.db"
