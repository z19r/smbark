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

# Format all Go source (gofmt + golines at 80 cols)
fmt:
    gofmt -w .
    PATH="$(go env GOPATH)/bin:$PATH" golines \
        --base-formatter=gofmt --max-len=80 \
        --shorten-comments -w . \
        || go run github.com/segmentio/golines@latest \
            --base-formatter=gofmt --max-len=80 \
            --shorten-comments -w .

# Verify formatting (fails on anything unformatted)
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    bad="$(gofmt -l .)"
    if [[ -n "$bad" ]]; then
        echo "$bad"
        exit 1
    fi
    export PATH="$(go env GOPATH)/bin:$PATH"
    if ! command -v golines >/dev/null; then
        go install github.com/segmentio/golines@latest
    fi
    bad="$(golines --base-formatter=gofmt --max-len=80 \
        --shorten-comments -l .)"
    if [[ -n "$bad" ]]; then
        echo "$bad"
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

# Release verification gate (same checks CI enforces)
release-check: fmt-check vet
    go build ./...
    go test ./...

# Preview what a release would do without changing anything
release-dry-run LEVEL:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ ! "{{ LEVEL }}" =~ ^(patch|minor|major)$ ]]; then
        echo "Usage: just release-dry-run patch|minor|major"; exit 1
    fi
    CURRENT=$(tr -d '[:space:]' < VERSION)
    echo "Current version: $CURRENT"
    echo "Bump level: {{ LEVEL }}"
    just release-check
    echo ""
    echo "All checks passed. Run: just release {{ LEVEL }}"

# Bump version, open a release PR, merge on green CI (requires: gh)
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
    git fetch --tags origin
    OLD=$(tr -d '[:space:]' < VERSION)
    IFS='.' read -r MAJ MIN PAT <<< "$OLD"
    case "{{ LEVEL }}" in
        patch) PAT=$((PAT + 1)) ;;
        minor) MIN=$((MIN + 1)); PAT=0 ;;
        major) MAJ=$((MAJ + 1)); MIN=0; PAT=0 ;;
    esac
    VERSION="${MAJ}.${MIN}.${PAT}"
    echo "Releasing v${OLD} -> v${VERSION}"
    printf '%s\n' "$VERSION" > VERSION
    # SECURITY.md names the current supported release.
    sed -i -E "s/is \*\*v[0-9]+\.[0-9]+\.[0-9]+\*\*/is **v${VERSION}**/" \
        SECURITY.md
    # Promote [Unreleased] to [VERSION] with auto-generated entries from
    # conventional commits since the last tag.
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
    CL_SECTION=$(mktemp)
    trap 'rm -f "$CL_SECTION"' EXIT
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
        if (( ! ${#ADDED[@]} && ! ${#FIXED[@]} && ! ${#CHANGED[@]} )); then
            echo ""
            echo "### Changed"
            echo ""
            echo "- version bump only (no conventional commits since ${PREV_TAG})"
        fi
    } > "$CL_SECTION"
    awk -v section="$CL_SECTION" '
        /^## \[Unreleased\]/ {
            while ((getline line < section) > 0) print line
            next
        }
        { print }
    ' CHANGELOG.md > CHANGELOG.md.tmp
    mv CHANGELOG.md.tmp CHANGELOG.md
    # Refresh the link-reference footer.
    REPO="https://github.com/z19r/smbark"
    if grep -q '^\[Unreleased\]:' CHANGELOG.md; then
        sed -i "s#^\[Unreleased\]:.*#[Unreleased]: ${REPO}/compare/v${VERSION}...HEAD#" \
            CHANGELOG.md
        printf '[%s]: %s/releases/tag/v%s\n' "$VERSION" "$REPO" "$VERSION" \
            >> CHANGELOG.md
    fi
    git checkout -b "release/v${VERSION}"
    git add VERSION CHANGELOG.md SECURITY.md
    git commit -m "release: v${VERSION}"
    git push -u origin "release/v${VERSION}"
    gh pr create \
        --title "release: v${VERSION}" \
        --body "Bump to v${VERSION} ({{ LEVEL }} release)" \
        --base main

    echo "Waiting for CI checks to appear..."
    for i in $(seq 1 30); do
        if gh pr checks --json name 2>/dev/null | grep -q name; then break; fi
        sleep 2
    done
    echo "Watching CI checks..."
    gh pr checks --watch --fail-fast

    # Cooper&Wright may auto-merge the PR first; only merge if still open.
    if [[ "$(gh pr view --json state -q .state)" == "OPEN" ]]; then
        echo "CI passed. Merging..."
        gh pr merge --squash --delete-branch
    fi

    git checkout main
    git pull --ff-only origin main

    echo "Watching release workflow..."
    sleep 5
    RUN_ID=$(gh run list --workflow release.yml --branch main --limit 1 \
        --json databaseId -q '.[0].databaseId')
    gh run watch "$RUN_ID" --exit-status

    echo ""
    echo "Release v${VERSION} complete."

# Cut a prerelease (beta) of the CURRENT VERSION: vX.Y.Z-beta.N.
# The beta number auto-increments from existing tags. VERSION is left
# untouched — betas lead up to the same X.Y.Z final. Usage: just prerelease
prerelease:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -n "$(git status --porcelain)" ]]; then
        echo "Error: dirty working tree"; exit 1
    fi
    BRANCH=$(git rev-parse --abbrev-ref HEAD)
    if [[ "$BRANCH" != "main" ]]; then
        echo "Error: prerelease must run from main (on $BRANCH)"; exit 1
    fi
    # Refuse to promote an empty [Unreleased] section.
    UNRELEASED=$(awk '
        /^## \[Unreleased\]/ { found=1; next }
        found && /^## \[/ { exit }
        found && /^- / { print; exit }
    ' CHANGELOG.md)
    if [[ -z "$UNRELEASED" ]]; then
        echo "Error: [Unreleased] section in CHANGELOG.md is empty."
        echo "Add changelog entries before releasing."
        exit 1
    fi
    # Release quality gate.
    go build ./...
    go vet ./...
    go test ./...
    # Beta of the current base version; next N from existing beta tags.
    BASE=$(tr -d '[:space:]' < VERSION)
    LAST=$(git tag -l "v${BASE}-beta.*" \
        | sed "s/^v${BASE}-beta\.//" | sort -n | tail -1)
    if [[ -z "${LAST:-}" ]]; then N=1; else N=$((LAST + 1)); fi
    PRE="${BASE}-beta.${N}"
    if git rev-parse -q --verify "refs/tags/v${PRE}" >/dev/null; then
        echo "Error: tag v${PRE} already exists"; exit 1
    fi
    TODAY=$(date -u +%Y-%m-%d)
    echo "Cutting prerelease v${PRE}"
    # Promote [Unreleased] into a dated beta section, leaving a fresh
    # empty Unreleased at the top. VERSION is intentionally not changed.
    awk -v ver="$PRE" -v today="$TODAY" '
        /^## \[Unreleased\]/ && !done {
            print "## [Unreleased]"
            print ""
            print "## [" ver "] - " today
            done = 1
            next
        }
        { print }
    ' CHANGELOG.md > CHANGELOG.md.tmp
    mv CHANGELOG.md.tmp CHANGELOG.md
    # Refresh the link-reference footer.
    REPO="https://github.com/z19r/smbark"
    if grep -q '^\[Unreleased\]:' CHANGELOG.md; then
        sed -i "s#^\[Unreleased\]:.*#[Unreleased]: ${REPO}/compare/v${PRE}...HEAD#" \
            CHANGELOG.md
        printf '[%s]: %s/releases/tag/v%s\n' "$PRE" "$REPO" "$PRE" \
            >> CHANGELOG.md
    fi
    git add CHANGELOG.md
    git commit -m "release: v${PRE}"
    git tag "v${PRE}"
    echo ""
    echo "Committed and tagged v${PRE} (VERSION stays ${BASE})."
    echo "Push with: git push origin main && git push origin v${PRE}"
    git push origin main
    git push origin "v${PRE}"

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
