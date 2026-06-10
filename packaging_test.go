package main

import (
	"os"
	"strings"
	"testing"
)

func TestPackagingUsesNiriAutolabelRuntimeNames(t *testing.T) {
	pkgbuild := mustReadText(t, "PKGBUILD")
	srcinfo := mustReadText(t, ".SRCINFO")
	readme := mustReadText(t, "README.md")
	service := mustReadText(t, "niri-autolabel.service")

	requireContains(t, pkgbuild, `go build -ldflags "-s -w -X main.version=$pkgver" -o "$pkgname" .`)
	requireContains(t, pkgbuild, `install -Dm755 "$pkgname" "$pkgdir/usr/bin/$pkgname"`)
	requireContains(t, pkgbuild, `install -Dm644 niri-autolabel.service "$pkgdir/usr/lib/systemd/user/niri-autolabel.service"`)
	requireContains(t, srcinfo, "pkgbase = niri-autolabel")
	requireContains(t, srcinfo, "pkgname = niri-autolabel")
	requireContains(t, readme, "systemctl --user enable --now niri-autolabel")
	requireContains(t, service, "ExecStart=/usr/bin/niri-autolabel")
}

func mustReadText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}
