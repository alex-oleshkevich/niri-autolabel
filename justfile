set shell := ["bash", "-uc"]

aur_repo := "ssh://aur@aur.archlinux.org/niri-autolabel.git"

# list recipes
default:
    @just --list

# build the binary
build:
    go build -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o autolabel .

# run the test suite
test:
    go test ./...

# vet + build + test
check: test
    go vet ./...
    go build ./...

# install to ~/.local/bin
install: build
    install -Dm755 autolabel ~/.local/bin/autolabel

# create and push a release tag (triggers the AUR GitHub workflow)
tag version:
    git tag -a "v{{version}}" -m "v{{version}}"
    git push origin "v{{version}}"

# bump PKGBUILD to a released tag, refresh checksums and .SRCINFO, push to the AUR
# (the v{{version}} tag must already exist on GitHub so its tarball is downloadable)
deploy-aur version:
    #!/usr/bin/env bash
    set -euo pipefail
    sed -i "s/^pkgver=.*/pkgver={{version}}/" PKGBUILD
    sed -i "s/^pkgrel=.*/pkgrel=1/" PKGBUILD
    updpkgsums PKGBUILD
    makepkg --printsrcinfo > .SRCINFO
    work="$(mktemp -d)"
    git clone "{{aur_repo}}" "$work"
    cp PKGBUILD .SRCINFO "$work/"
    git -C "$work" add PKGBUILD .SRCINFO
    git -C "$work" commit -m "Update to {{version}}"
    git -C "$work" push
    rm -rf "$work"
