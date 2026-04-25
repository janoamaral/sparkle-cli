# GitHub Actions - Build and Release

Two GitHub Actions workflows have been configured to automate builds and release creation:

## Option 1: `build-release.yml` - Commit Message Based (Recommended for your case)

**Trigger:** Push to the `main` branch

**Detection:** Looks for a `v1.0.1` pattern in the commit message

**Usage:**
```bash
git commit -m "Merge pull request... v1.0.2"
git push origin main
```

The workflow:
1. ✅ Extracts the version from the commit message
2. ✅ Builds binaries for Linux, macOS (Intel and ARM), and Windows
3. ✅ Automatically creates a release with that version
4. ✅ Automatically generates release notes by comparing commits
5. ✅ Uploads all build artifacts (binaries) to the release

**Expected commit format:**
- ✅ `v1.0.1` (simple)
- ✅ `Release v1.0.1`
- ✅ `Merge branch... v1.0.1`

---

## Option 2: `release-by-tag.yml` - Git Tag Based

**Trigger:** When a tag with format `v1.0.1` is created

**Usage:**
```bash
git tag v1.0.2
git push origin v1.0.2
```

Or from GitHub:
1. Go to Releases
2. Click "Draft a new release"
3. Enter the tag (v1.0.2)
4. GitHub Actions will automatically create the release

**Advantage:** More controlled and the industry-standard approach

---

## Prerequisites

✅ Workflows are already configured  
✅ They use the existing go.mod (Go 1.26.2)  
✅ `./cmd/sparkle-cli/main.go` is compiled  

## Variables and Configuration

If you need to change anything:
- **Go version:** Update `1.26.2` in the workflow
- **Output binaries:** Use format `sparkle-cli-{OS}-{ARCH}`
- **Platforms:** Linux amd64, macOS Intel (amd64), macOS ARM (arm64), Windows amd64

## Automatic Release Notes

GitHub automatically generates release notes by comparing:
- Commits since the previous release
- Merged PRs
- Dependency changes

## Generated Artifacts

Each release includes:
- `sparkle-cli-linux-amd64` - Linux 64-bit
- `sparkle-cli-darwin-amd64` - macOS Intel
- `sparkle-cli-darwin-arm64` - macOS Apple Silicon
- `sparkle-cli-windows-amd64.exe` - Windows 64-bit

---

## Recommendation

Use **Option 1** (`build-release.yml`) if you prefer versioning to be part of the commit message.

Use **Option 2** (`release-by-tag.yml`) if you prefer the standard Git tag flow (recommended long-term).

You can keep both active simultaneously without conflicts.
