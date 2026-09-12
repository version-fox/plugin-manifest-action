# Shared release workflows for vfox plugins

[中文文档](README.md)

Each plugin keeps its own repository, version, and GitHub releases. This repository
maintains the common validation, packaging, and publishing implementation.
Updating this tool does **not** publish any plugin.

## Getting started

Copy [example-workflow.yml](example-workflow.yml) to
`.github/workflows/publish.yaml` in your plugin repository. It includes PR checks,
manual releases, and support for existing `vX.Y.Z` tag pushes. The caller uses:

```yaml
jobs:
  check:
    if: github.event_name == 'pull_request'
    uses: version-fox/plugin-manifest-action/.github/workflows/check-plugin.yml@v1
  release:
    if: github.event_name != 'pull_request'
    permissions:
      contents: write
    uses: version-fox/plugin-manifest-action/.github/workflows/release-plugin.yml@v1
    with:
      version: ${{ inputs.version || '' }}
```

Use the complete example for triggers, inputs, and default permissions. Merge it
into the default branch, then open **Actions → Plugin → Run workflow**. Enter a
stable version such as `0.5.5`, without `v`. The workflow updates `metadata.lua`,
commits the version, atomically pushes the commit and tag, and publishes the ZIP
and manifests in the **calling plugin repository**. No local tag is necessary.
Tag releases require matching metadata and do not modify tagged source.
Prerelease versions are not supported. PR titles never trigger publication.

## Permissions, dependencies, and updates

Validation uses `contents: read`. Publishing uses the calling repository's
`GITHUB_TOKEN` with `contents: write`. No personal token or GitHub App is required.
Repository rules must allow version commits and tags; the tool does not bypass
branch or tag protections.

The new workflows use Go and vfox's Lua 5.1 interpreter. They do not install system
Lua, LuaRocks, or dkjson through `apt-get`. Third-party Actions are pinned to commit
SHAs. Optional formatting uses a fixed StyLua version.

`@v1` follows compatible tool releases. Pin a published concrete version, such as
`@v1.0.2`, to review updates individually. Concrete tags do not move. The workflow
uses `job.workflow_sha` to check out its own exact tool source. This requires
GitHub.com; GitHub Enterprise Server is not currently supported.
Common tool updates affect future workflow runs only. They never release plugins
automatically. Dependabot is not required for callers following `@v1`.

## Package and check contract

- Use `metadata.lua` + `hooks/`; the `main.lua` layout is not supported.
- Metadata must define name, version, homepage, license, and description.
  Automatic versioning requires one standalone `PLUGIN.version = "X.Y.Z"` assignment.
- Required hook files must directly define `Available`, `PreInstall`, and `EnvKeys`.
- Packages include `metadata.lua`, `hooks/`, `lib/`, `bin/`, and optional `LICENSE`,
  `LICENSE.md`, or `LICENSE.txt`. Put runtime data under `lib/` and helpers under
  `bin/`. Other root files are excluded. Symlinks are rejected.
- ZIP ordering, timestamps, and permissions are normalized. Executable helpers
  under `bin/` retain executable permissions. Manifests include the ZIP's SHA-256.
- Checks load metadata and validate Lua syntax and required hooks. They do not
  execute lifecycle hooks or install SDKs, and do not replace behavior tests.

To opt into format checks, add `stylua.toml` at the plugin root and format with
StyLua **2.5.2**. The template provides a Lua 5.1 configuration. The shared check
runs `stylua --check`, without rewriting or committing files. Repositories without
that configuration retain their existing checks.

## Recovery

Use **Re-run failed jobs** on the original run. Retries require the original source
or its exact version-only child commit. They verify matching draft assets and
upload missing assets; published version assets are not overwritten.

Once the version release and downloads are verified, the tool updates the mutable
`manifest` release used by existing clients. Older retries cannot roll it back.
If this update fails, rerun the same release. Mutable asset replacement may make
that asset briefly unavailable; version-specific assets remain available.
Publication is serialized per plugin repository.

## Development and verification

Use the Go version in [go.mod](go.mod):

```sh
go test ./... -race
GOTOOLCHAIN=auto go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
go run ./cmd/plugin-release check \
  --source ../vfox-nodejs --repository version-fox/vfox-nodejs --out /tmp/nodejs-check
```

Default tests use temporary Git repositories and in-memory release storage to
exercise publication, failures, and retries. CI also checks packages from public
official plugins and the template. These checks do not install SDKs.
An optional read-only test verifies the actual GitHub lookup protocol:

```sh
VFOX_TEST_GITHUB_REPOSITORY=version-fox/vfox-nodejs \
  go test ./internal/release -run TestGitHubReadOnlyProtocol -v
```

After merging tool changes, run **Actions → Release Tool → Run workflow** with a
new stable tool version. It runs checks, updates `VERSION`, publishes the concrete
release, and advances the corresponding major tag. Incompatible changes require
a new major version.

## Legacy composite Action

The root [action.yml](action.yml), used through
`uses: version-fox/plugin-manifest-action@main`, retains its old implementation for
compatibility. It still uses Lua/LuaRocks and does not automatically gain the new
workflow's version commits, deterministic packaging, or retry guarantees.
Migrate to the reusable workflow example to use the new implementation. Changing
only the version suffix on the root Action does not perform this migration.

## License

Apache 2.0.
