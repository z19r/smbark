# SMBark — project command runner
# https://github.com/casey/just

set dotenv-load := false

version := `cat VERSION 2>/dev/null || echo "0.0.0"`

# ─── Default ──────────────────────────────────────────────────────

default:
    @just --list --unsorted

# ─── Development ──────────────────────────────────────────────────

# Build debug binary
build:
    go build -o smbark .

# Build optimized release binary with version info
build-release:
    go build -ldflags="-s -w" -o smbark .

# Install smbark to $GOPATH/bin
install:
    go install .

# Run smbark
run: build
    ./smbark

# Watch for changes and rebuild on save
watch:
    watchexec -e go -- go build -o smbark .

# Check compilation without producing binaries
check:
    go vet ./...
    go build ./...

# ─── Testing ─────────────────────────────────────────────────────

# Run all tests
test:
    go test ./...

# Run a single test by name
test-one NAME:
    go test ./... -run {{ NAME }} -v

# Run tests with verbose output
test-verbose:
    go test -v ./...

# Run tests with race detector
test-race:
    go test -race ./...

# Generate HTML coverage report
test-coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Report: coverage.html"

# Show coverage percentage and fail if below threshold
test-coverage-check THRESHOLD="80":
    #!/usr/bin/env bash
    set -euo pipefail
    go test -coverprofile=coverage.out ./...
    COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | tr -d '%')
    echo "Coverage: ${COVERAGE}%"
    if (( $(echo "$COVERAGE < {{ THRESHOLD }}" | bc -l) )); then
        echo "FAIL: coverage ${COVERAGE}% below threshold {{ THRESHOLD }}%"
        exit 1
    fi

# ─── Linting & Formatting ───────────────────────────────────────

# Format all source files
fmt:
    gofmt -w .

# Check formatting without modifying files
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    UNFORMATTED=$(gofmt -l .)
    if [[ -n "$UNFORMATTED" ]]; then
        echo "Unformatted files:"
        echo "$UNFORMATTED"
        exit 1
    fi

# Run go vet
vet:
    go vet ./...

# Run golangci-lint
lint:
    golangci-lint run ./...

# Lint everything (format check + vet + lint)
lint-all: fmt-check vet lint

# Fix formatting + tidy modules
fix: fmt tidy

# ─── Dependencies ───────────────────────────────────────────────

# Tidy module dependencies
tidy:
    go mod tidy

# Show the dependency graph
deps:
    go mod graph

# Show direct dependencies only
deps-direct:
    @grep -v '// indirect' go.mod | grep '\t' || true

# Check for known vulnerabilities
audit:
    govulncheck ./...

# ─── Pre-commit & CI ────────────────────────────────────────────

# Quick pre-commit checks (format + vet + build)
pre-commit: fmt vet check

# Full local CI pipeline (lint-all + all tests)
ci-local: lint-all test

# ─── Release ─────────────────────────────────────────────────────

# Release quality gate (fmt-check + vet + test)
release-check:
    #!/usr/bin/env bash
    set -euo pipefail
    UNFORMATTED=$(gofmt -l .)
    if [[ -n "$UNFORMATTED" ]]; then
        echo "Unformatted files:"; echo "$UNFORMATTED"; exit 1
    fi
    go vet ./...
    go test ./...

# Preview what a release would do without changing anything
release-dry-run LEVEL:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! "{{ LEVEL }}" =~ ^(patch|minor|major)$ ]]; then
        echo "Usage: just release-dry-run patch|minor|major"; exit 1
    fi
    CURRENT=$(cat VERSION 2>/dev/null || echo "0.0.0")
    echo "Current version: $CURRENT"
    echo "Bump level: {{ LEVEL }}"
    just release-check
    echo ""
    echo "All checks passed. Run: just release {{ LEVEL }}"

# Bump version, create release branch + PR (requires: gh)
release LEVEL: release-check
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! "{{ LEVEL }}" =~ ^(patch|minor|major)$ ]]; then
        echo "Usage: just release patch|minor|major"; exit 1
    fi
    if [[ -n "$(git status --porcelain)" ]]; then
        echo "Error: dirty working tree"; exit 1
    fi
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    if [[ "$BRANCH" != "main" ]]; then
        read -r -p "Not on main (currently on $BRANCH). Switch to main? [y/N] " REPLY || REPLY=""
        if [[ "$REPLY" =~ ^[Yy]$ ]]; then
            git checkout main
        else
            echo "Aborted: release must run from main"; exit 1
        fi
    fi
    git pull --ff-only origin main
    OLD_VERSION=$(cat VERSION 2>/dev/null || echo "0.0.0")
    IFS='.' read -r MAJOR MINOR PATCH <<< "$OLD_VERSION"
    case "{{ LEVEL }}" in
        major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
        minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
        patch) PATCH=$((PATCH + 1)) ;;
    esac
    VERSION="${MAJOR}.${MINOR}.${PATCH}"
    echo "$VERSION" > VERSION
    sed -i -E "s/is \*\*v[0-9]+\.[0-9]+\.[0-9]+\*\*/is **v${VERSION}**/" SECURITY.md
    go build ./...
    TODAY=$(date -u +%Y-%m-%d)
    PREV_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    if [[ -n "$PREV_TAG" ]]; then
        COMMIT_LOG=$(git log "${PREV_TAG}..HEAD" --pretty=format:"%s" --no-merges | grep -v '^release:' || true)
    else
        COMMIT_LOG=$(git log --pretty=format:"%s" --no-merges | grep -v '^release:' || true)
    fi
    ADDED=()
    FIXED=()
    CHANGED=()
    RE_FEAT='^feat(\([^)]*\))?!?:[[:space:]](.+)'
    RE_FIX='^fix(\([^)]*\))?!?:[[:space:]](.+)'
    RE_OTHER='^(refactor|perf|chore|ci|docs|test|build|style)(\([^)]*\))?!?:[[:space:]](.+)'
    while IFS= read -r msg; do
        [[ -z "$msg" ]] && continue
        if [[ "$msg" =~ $RE_FEAT ]]; then
            ADDED+=("${BASH_REMATCH[2]}")
        elif [[ "$msg" =~ $RE_FIX ]]; then
            FIXED+=("${BASH_REMATCH[2]}")
        elif [[ "$msg" =~ $RE_OTHER ]]; then
            CHANGED+=("${BASH_REMATCH[3]}")
        fi
    done <<< "$COMMIT_LOG"
    {
        echo "## [Unreleased]"
        echo ""
        echo "## [${VERSION}] - ${TODAY}"
        if (( ${#ADDED[@]} )); then
            echo ""
            echo "### Added"
            echo ""
            for b in "${ADDED[@]}"; do echo "- $b"; done
        fi
        if (( ${#FIXED[@]} )); then
            echo ""
            echo "### Fixed"
            echo ""
            for b in "${FIXED[@]}"; do echo "- $b"; done
        fi
        if (( ${#CHANGED[@]} )); then
            echo ""
            echo "### Changed"
            echo ""
            for b in "${CHANGED[@]}"; do echo "- $b"; done
        fi
    } > /tmp/smbark_cl_section
    awk '
        /^## \[Unreleased\]/ {
            while ((getline line < "/tmp/smbark_cl_section") > 0) print line
            next
        }
        { print }
    ' CHANGELOG.md > CHANGELOG.md.tmp
    mv CHANGELOG.md.tmp CHANGELOG.md
    rm -f /tmp/smbark_cl_section
    git checkout -b "release/v${VERSION}"
    git add VERSION CHANGELOG.md SECURITY.md
    git commit -m "release: v${VERSION}"
    git push -u origin "release/v${VERSION}"
    gh pr create \
        --title "release: v${VERSION}" \
        --body "Bump to v${VERSION} ({{ LEVEL }} release)" \
        --base main
    echo "Waiting for CI checks to appear..."
    HAS_CHECKS=false
    for i in $(seq 1 15); do
        if gh pr checks --json name 2>/dev/null | grep -q name; then
            HAS_CHECKS=true; break
        fi
        sleep 2
    done
    if $HAS_CHECKS; then
        echo "Watching CI checks..."
        gh pr checks --watch --fail-fast
    else
        echo "No CI checks configured, skipping."
    fi
    echo "Merging..."
    gh pr merge --squash --delete-branch
    git checkout main
    git pull --ff-only origin main
    git tag -a "v${VERSION}" -m "v${VERSION}"
    git push origin "v${VERSION}"
    echo ""
    echo "Release v${VERSION} tagged. GitHub Actions will build binaries, create the release, and (if PUBLISH_TO_AUR is enabled) publish to the AUR."
    echo "  https://github.com/z19r/smbark/actions"

# ─── Cleanup ─────────────────────────────────────────────────────

# Remove build artifacts
clean:
    rm -f smbark coverage.out coverage.html
    rm -rf dist

# ─── Site ────────────────────────────────────────────────────────

# Start local site dev server
site-dev:
    cd site && python3 -m http.server 8080

# Deploy site to production
site-deploy:
    netlify deploy --prod --dir=site

# Preview site deployment
site-preview:
    netlify deploy --dir=site

# ─── Project Info ────────────────────────────────────────────────

# Show project and toolchain versions
info:
    @echo "smbark v{{ version }}"
    @echo ""
    @echo "Toolchain"
    @echo "  go:             $(go version)"
    @echo "  just:           $(just --version)"
    @echo ""
    @echo "Dev Tools"
    @echo "  golangci-lint:  $(golangci-lint --version 2>/dev/null | head -1 || echo 'not installed')"
    @echo "  govulncheck:    $(govulncheck -version 2>/dev/null | head -1 || echo 'not installed')"

# Show lines of code
loc:
    @echo "Source:"
    @find . -name '*.go' -not -path './vendor/*' | xargs wc -l | tail -1
