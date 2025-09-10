package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/google/go-github/v62/github"
	"golang.org/x/oauth2"
)

// staticToken is a fallback token.
// IMPORTANT: For security, it's best to leave this empty and use flags or environment variables.
// If you must embed a token, be aware of the security risks.
const staticToken = ""

// releaseCandidate holds information about a downloadable release asset.
type releaseCandidate struct {
	RepoOwner   string
	RepoName    string
	AssetName   string
	DownloadURL string
	AssetID     int64
}

func main() {

	// 1. Argument and Flag Parsing
	tokenFlag := flag.String("token", "", "GitHub personal access token.")
	publicFlag := flag.Bool("public", false, "Search public repositories.")
	flag.Parse()

	repoPattern := ""
	if len(flag.Args()) > 0 {
		repoPattern = strings.ToLower(flag.Args()[0])
	}

	versionPattern := ""
	if len(flag.Args()) > 1 {
		versionPattern = strings.ToLower(flag.Args()[1])
	}

	// 2. Token Acquisition
	token := getToken(*tokenFlag)
	if token == "" {
		fmt.Println("GitHub token not found. Provide one via -token flag or GH_TOKEN env var.")
		return
	}

	// 3. Platform Verification
	platformArch := runtime.GOARCH
	platformOS := runtime.GOOS
	if platformOS != "linux" || (platformArch != "amd64" && platformArch != "arm64") {
		fmt.Fprintf(os.Stderr, "This program is designed to run only on linux/amd64 or linux/arm64. Detected: %s/%s\n", platformOS, platformArch)
		os.Exit(1)
	}

	// 4. GitHub Client Initialization
	ctx := context.Background()
	// Create a new token source
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	// Create a new HTTP client with the token source
	tc := oauth2.NewClient(ctx, ts)
	// Create a new GitHub client
	client := github.NewClient(tc)

	// 5. Find Release Candidates

	candidates, err := findReleaseCandidates(ctx, client, repoPattern, versionPattern, platformOS, platformArch, *publicFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding releases: %v\n", err)
		os.Exit(1)
	}

	// 6. Action based on number of candidates
	switch len(candidates) {
	case 0:
		fmt.Println("No matching release artifacts found for your platform.")
	case 1:
		c := candidates[0]
		fmt.Printf("%s/%s: %s\n", c.RepoOwner, c.RepoName, c.AssetName)
		if err := downloadAndPrepare(ctx, client, tc, c); err != nil {
			fmt.Println("failed")
			fmt.Fprintf(os.Stderr, "Error downloading and preparing artifact: %v\n", err)
			os.Exit(1)
		}
	default:

		for _, c := range candidates {
			fmt.Printf("%s/%s: %s\n", c.RepoOwner, c.RepoName, c.AssetName)
		}
	}
}

// getToken resolves the GitHub token from flag, environment variable, or static constant.
func getToken(tokenFlag string) string {
	if tokenFlag != "" {
		return tokenFlag
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token
	}
	if staticToken != "" {
		return staticToken
	}
	return ""
}

// findReleaseCandidates searches through repositories to find matching release assets.
func findReleaseCandidates(ctx context.Context, client *github.Client, pattern, versionPattern, os, arch string, public bool) ([]releaseCandidate, error) {
	var candidates []releaseCandidate
	var repos []*github.Repository

	if public {
		user, _, err := client.Users.Get(ctx, "")
		if err != nil {
			return nil, err
		}
		opts := &github.RepositoryListByUserOptions{
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			r, resp, err := client.Repositories.ListByUser(ctx, user.GetLogin(), opts)
			if err != nil {
				return nil, err
			}
			repos = append(repos, r...)
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	} else {
		opts := &github.RepositoryListOptions{
			Visibility:  "private",
			ListOptions: github.ListOptions{PerPage: 100},
		}
		for {
			r, resp, err := client.Repositories.List(ctx, "", opts)
			if err != nil {
				return nil, err
			}
			repos = append(repos, r...)
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}

	for _, repo := range repos {
		repoName := repo.GetName()
		repoOwner := repo.GetOwner().GetLogin()

		// Filter by repository name pattern if provided
		if pattern != "" && !strings.Contains(strings.ToLower(repoName), pattern) {
			continue
		}

		// Get the release for the repository
		var release *github.RepositoryRelease
		if versionPattern == "" {
			// If no version pattern is provided, get the latest release
			var err error
			release, _, err = client.Repositories.GetLatestRelease(ctx, repoOwner, repoName)
			if err != nil {
				// This often returns 404 if no releases exist. We can safely ignore it.
				continue
			}
		} else {
			// If a version pattern is provided, find the matching release
			releases, _, err := client.Repositories.ListReleases(ctx, repoOwner, repoName, nil)
			if err != nil {
				continue
			}
			for _, r := range releases {
				if strings.Contains(strings.ToLower(r.GetTagName()), versionPattern) {
					release = r
					break
				}
			}
			if release == nil {
				continue
			}
		}

		// Find a matching asset in the release
		for _, asset := range release.Assets {
			assetName := strings.ToLower(asset.GetName())
			if strings.Contains(assetName, os) && strings.Contains(assetName, arch) {

				candidates = append(candidates, releaseCandidate{
					RepoOwner:   repoOwner,
					RepoName:    repoName,
					AssetName:   asset.GetName(),
					DownloadURL: asset.GetBrowserDownloadURL(),
					AssetID:     asset.GetID(),
				})
				break // Found a match for this repo, move to the next one
			}
		}
	}

	return candidates, nil
}

// downloadAndPrepare downloads the given asset, saves it, and makes it executable.
func downloadAndPrepare(ctx context.Context, client *github.Client, httpClient *http.Client, c releaseCandidate) error {
	// 1. Download the asset content using the authenticated client
	rc, _, err := client.Repositories.DownloadReleaseAsset(ctx, c.RepoOwner, c.RepoName, c.AssetID, httpClient)
	if err != nil {
		return fmt.Errorf("could not download asset content: %w", err)
	}
	defer rc.Close()

	// 2. Create the output file
	out, err := os.Create(c.AssetName)
	if err != nil {
		return fmt.Errorf("could not create file %s: %w", c.AssetName, err)
	}
	defer out.Close()

	// 3. Write the body to the file
	_, err = io.Copy(out, rc)
	if err != nil {
		return fmt.Errorf("could not write to file: %w", err)
	}
	fmt.Println("downloaded")

	// 4. Make the file executable (chmod +x)
	// 0755 is rwxr-xr-x
	if err := os.Chmod(c.AssetName, 0755); err != nil {
		return fmt.Errorf("could not make file executable: %w", err)
	}
	fmt.Println("made executable")

	return nil
}
