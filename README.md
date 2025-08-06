# notionmd-cli
A tool that wraps Notion MD to allow manipulation from the command line

## Build Instructions

You need Go 1.18+ installed.

```sh
# Clone the repo (if you haven't already)
git clone <repo-url>
cd notionmd-cli

# Build the binary
go build -o notionmd-cli
```

## Usage

```sh
notionmd-cli -token <token> -page <page_id> -md <markdown-file> [--append|--replace]
```

- `--append`: Append content to the bottom of the existing Notion page (default behavior).
- `--replace`: Remove all current content before adding new content.

Example:
```sh
notionmd-cli -token $NOTION_TOKEN -page <page_id> -md notes.md --replace
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
- Ensure you have set up a `.goreleaser.yml` file (already present in this repo).
- (Optional) Set up a GitHub token for publishing:  
  ```sh
  export GITHUB_TOKEN=your_token
  ```

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
