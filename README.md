# get_gh_release

`get_gh_release` is a command-line tool to find and download the latest release artifacts from private GitHub repositories. It supports filtering by repository name and automatically selects artifacts matching the current Linux platform (amd64 or arm64).

## Usage

```bash
./get_gh_release [repository_name_pattern]
```

### Authentication

The tool requires a GitHub Fine-Grained Personal Access Token (PAT) with `repo` scope (or at least `contents:read` for private repositories). The token is obtained in the following order of precedence:

1.  From the `-token` command-line flag:
    ```bash
    ./get_gh_release -token your_github_pat
    ```
2.  From the `GH_TOKEN` environment variable:
    ```bash
    export GH_TOKEN="your_github_pat"
    ./get_gh_release
    ```
3.  (Optional) From a hardcoded `staticToken` constant in `main.go` (not recommended for security).

### Examples

**Download an artifact from a specific repository:**

If only one private repository matches "my-app" and has a Linux artifact for your architecture, it will be downloaded and made executable.

```bash
./get_gh_release my-app
```

**List matching artifacts if multiple are found:**

If multiple repositories match "tool-", or if a single match has multiple artifacts for your platform, the tool will list them.

```bash
./get_gh_release tool-
```

**Download without a repository filter:**

The tool will scan all private repositories accessible by your token. If only one matching artifact is found across all repos, it will be downloaded. Otherwise, a list will be provided.

```bash
./get_gh_release
```

## Build

To build the tool from source:

```bash
go build .
```
