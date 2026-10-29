# Changelog

All notable changes to smbark will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.2.1] - 2026-08-19

### Added

- publish smbark to the AUR on release (#10)
- add attestations (#8)

### Changed

- add sysExec seam and comprehensive test suite (#11)
- add security policy (#9)

## [1.2.0] - 2026-08-18

### Added

- add version command and show version in help menu (#6)

## [1.1.0] - 2026-08-18

## [1.0.1] - 2026-08-17

### Fixed

- negotiate SMB dialect instead of hard-coding vers=3.0 (#3)

## [1.0.0] - 2026-08-12

### Added

- add release flow with cross-compiled binaries and install script

### Changed

- use GoReleaser for release builds
- add GitHub Actions for CI and release
- add MIT license and link the project site
- add project README

## [0.1.0] - 2026-08-12

### Added

- SMB share manager TUI with Bubble Tea
- TOML configuration support
- Fuzzy-search share filtering
- Project site with Netlify deployment

[Unreleased]: https://github.com/z19r/smbark/compare/v1.2.1...HEAD
[1.0.0]: https://github.com/z19r/smbark/releases/tag/v1.0.0
[1.0.1]: https://github.com/z19r/smbark/releases/tag/v1.0.1
[1.1.0]: https://github.com/z19r/smbark/releases/tag/v1.1.0
[1.2.0]: https://github.com/z19r/smbark/releases/tag/v1.2.0
[1.2.1]: https://github.com/z19r/smbark/releases/tag/v1.2.1
