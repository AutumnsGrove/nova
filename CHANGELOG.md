# Changelog

All notable changes to Nova are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `nova update --target <path>` and `NOVA_INSTALL_DIR` choose which binary `nova update` replaces, and the
  command prints the target it is using.
- Styled `--help` and `nova version` with JSON output.
- First-run flow: log in or paste a key, then pick a model.
- `nova doctor` for checking the installation.
- `nova docs` topics and ranked search, including the examples and the SDK.
- The built-in `extension-author` skill.
- Ten example extensions under `examples/extensions/`.
- A favicon variant of the logo.
- A release workflow with checksummed binaries and `install.sh`.

### Changed

- `/login` shows a provider as ready when its key comes from the config, and the OAuth sign-in dialog shows the
  loopback callback status. After a login the model picker opens only when the current model needs it, and
  `/logout` from the provider serving the current model switches to another signed-in provider's model.
- `list_providers` reports `current` and `default_model`, and counts a key from the loaded config as ready.

### Fixed

- `nova update` under `go run` replaced a temporary binary and reported success; it now refuses and says to
  pass `--target` or set `NOVA_INSTALL_DIR`.

## [0.1.0] - 2026-10-09

### Added

- The first release: the agent loop, tools, sessions, config, the TUI, the
  extension host, the bundled extensions, serve mode, and the GitHub Action.

[Unreleased]: https://github.com/AutumnsGrove/nova/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/AutumnsGrove/nova/releases/tag/v0.1.0
