# notionmd-cli
A CLI tool to manipulate Notion pages from Markdown files, wrapping Notion MD.

## Build Instructions

You need Go 1.18+ installed.

```sh
# Clone the repo (if you haven't already)
git clone <repo-url>
cd notionmd-cli

# Build the binary
go build -o notionmd-cli
```

To override the version (shown with --version/-v), build with:

```sh
go build -ldflags "-X main.Version=1.2.3" -o notionmd-cli
```
Replace `1.2.3` with your desired version string.

## Usage

You can run directly with Go or use the compiled binary.

### With go run
```sh
go run main.go --token <token> --page <page_id> --md <markdown-file> [flags]
```

### With the binary
```sh
./notionmd-cli --token <token> --page <page_id> --md <markdown-file> [flags]
```

### Flags
- `--token` (required): Notion integration token
- `--page` (required): Target Notion page ID
- `--md` (required): Path to markdown file
- `--append`: Append content to the bottom of the existing Notion page (default)
- `--replace`: Replace all existing content with new content
- `--use-hash`: Store and check content hash in a dedicated metadata block and/or property
- `--hash-property <name>`: Optionally specify property name for content hash (e.g. `--hash-property=MyPropName`)
- `--rewrite-text <mapping.json>`: Path to JSON file mapping text to rewrite in the markdown file (see below)
- `--dry-run`: Run all logic except Notion sync (no changes will be made to Notion)
- `--debug`: Enable debug output to stdout
- `--version`, `-v`: Print program version and exit

### Examples

#### Basic usage (append, default):
```sh
./notionmd-cli --token $NOTION_TOKEN --page <page_id> --md notes.md
```

#### Replace all content:
```sh
./notionmd-cli --token $NOTION_TOKEN --page <page_id> --md notes.md --replace
```

#### Using go run:
```sh
go run main.go --token $NOTION_TOKEN --page <page_id> --md notes.md --replace --debug
```

#### With rewrite mapping:
```sh
./notionmd-cli --token $NOTION_TOKEN --page <page_id> --md notes.md --rewrite-text notion-links.json
```

### Rewrite Mapping JSON Format
- Single page mapping:
  ```json
  {
    "TEST_REPLACE": "THIS_HAS_BEEN_REPLACED"
  }
  ```
- Multi-page mapping:
  ```json
  {
    "docs/installation.md": {
      "TEST_REPLACE": "THIS_HAS_BEEN_REPLACED"
    },
    "docs/other.md": {
      "FOO": "BAR"
    }
  }
  ```

## Releasing with GoReleaser

This project uses [goreleaser](https://goreleaser.com/) for publishing releases.

### Requirements
- Install GoReleaser:
  ```sh
  brew install goreleaser
  # or
  curl -sL https://git.io/goreleaser | bash
  ```
- Ensure you have a `.goreleaser.yml` file (already present in this repo).
- (Optional) Set up a GitHub token for publishing:
  ```sh
  export GITHUB_TOKEN=your_token
  ```

### Tagging and Releasing a New Version
1. Bump your version in code or via git tag (e.g. `git tag v1.2.3`)
2. Push the tag to GitHub:
   ```sh
   git push origin v1.2.3
   ```
3. Run GoReleaser:
   ```sh
   goreleaser release --clean
   ```
   or for a dry run:
   ```sh
   goreleaser release --clean --skip-publish --skip-validate --snapshot
   ```

See the [GoReleaser docs](https://goreleaser.com/quick-start/) for more info.

### Build and Test Locally
```sh
goreleaser build --clean
```

### Create a Release
This will build, create archives, and publish to GitHub Releases:
```sh
goreleaser release --clean
```

For a dry run (no publishing):
```sh
goreleaser release --clean --skip-publish --snapshot
```

See [goreleaser.com](https://goreleaser.com/quick-start/) for more info.
