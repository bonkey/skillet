targets := "darwin/arm64 darwin/amd64 linux/arm64 linux/amd64"

# List the recipes
default:
    @just --list

# The checks every commit and every release must pass
check:
    test -z "$(gofmt -l .)"
    go vet ./...
    go test ./...

# Build ./skillet for this machine
build:
    go build -o skillet .

# Install skillet into GOBIN
install:
    go install .

# Cross-compile archives and checksums for VERSION (without the leading v) into dist/
dist VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf dist && mkdir -p dist
    for target in {{targets}}; do
        os=${target%/*} arch=${target#*/}
        name="skillet_{{VERSION}}_${os}_${arch}"
        mkdir -p "dist/$name"
        CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
            -ldflags "-s -w -X main.version=v{{VERSION}}" -o "dist/$name/skillet" .
        cp README.md "dist/$name/"
        # The binary sits at the archive root, where mise's github backend looks for it.
        # COPYFILE_DISABLE keeps macOS tar from adding ._* metadata files.
        COPYFILE_DISABLE=1 tar -C "dist/$name" -czf "dist/$name.tar.gz" skillet README.md
        rm -rf "dist/$name"
    done
    (cd dist && shasum -a 256 *.tar.gz > checksums.txt)
    ls -1 dist

# Release VERSION: check, tag, push, build archives, publish (for example: just release 0.1.0)
release VERSION: (tag VERSION) (dist VERSION) (publish VERSION)

# Verify the release preconditions, then create and push the signed tag vVERSION
tag VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    tag="v{{VERSION}}"
    [[ "{{VERSION}}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "VERSION must look like 1.2.3"; exit 1; }
    [[ "$(git branch --show-current)" == "main" ]] || { echo "release from main"; exit 1; }
    [[ -z "$(git status --porcelain)" ]] || { echo "the working tree is not clean"; exit 1; }
    git fetch --quiet --tags origin
    [[ -z "$(git tag --list "$tag")" ]] || { echo "$tag already exists"; exit 1; }
    [[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || { echo "main differs from origin/main: push or pull first"; exit 1; }
    just check
    git tag --sign "$tag" --message "skillet $tag"
    git push origin "$tag"

# Create the GitHub release for the pushed tag vVERSION from dist/
publish VERSION:
    gh release create "v{{VERSION}}" dist/*.tar.gz dist/checksums.txt --verify-tag --generate-notes --title "v{{VERSION}}"
