package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
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
	log.SetFlags(0)

	// 1. Argument and Flag Parsing
	tokenFlag := flag.String("token", "", "GitHub personal access token.")
	flag.Parse()

	repoPattern := ""
	if len(flag.Args()) > 0 {
		repoPattern = strings.ToLower(flag.Args()[0])
	}

	// 2. Token Acquisition
	token := getToken(*tokenFlag)
	if token == "" {
		log.Println("GitHub token not found. Please provide one via -token flag or GH_TOKEN env var.")
		return
	}

	// 3. Platform Verification
	platformArch := runtime.GOARCH
	platformOS := runtime.GOOS
	if platformOS != "linux" || (platformArch != "amd64" && platformArch != "arm64") {
		log.Fatalf("This program is designed to run only on linux/amd64 or linux/arm64. Detected: %s/%s", platformOS, platformArch)
	}

	// 4. GitHub Client Initialization
	ctx := context.Background()
	// InsecureSkipVerify is added for cases where corporate proxies might intercept TLS.
	// In a secure environment, you might remove this.
	insecureClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, insecureClient)
	client := github.NewClient(nil).WithAuthToken(token)

	// 5. Find Release Candidates
	candidates, err := findReleaseCandidates(ctx, client, repoPattern, platformOS, platformArch)
	if err != nil {
		log.Fatalf("Error finding releases: %v", err)
	}

	// 6. Action based on number of candidates
	switch len(candidates) {
	case 0:
		log.Println("No matching release artifacts found for your platform.")
	case 1:
		c := candidates[0]
		log.Printf("Found one matching artifact: %s in repo %s/%s. Downloading...", c.AssetName, c.RepoOwner, c.RepoName)
		if err := downloadAndPrepare(ctx, client, insecureClient, c); err != nil {
			log.Fatalf("Failed to download and prepare artifact: %v", err)
		}
		log.Printf("Success! Artifact '%s' is downloaded and executable.", c.AssetName)
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
func findReleaseCandidates(ctx context.Context, client *github.Client, pattern, os, arch string) ([]releaseCandidate, error) {
	var candidates []releaseCandidate
	opts := &github.RepositoryListOptions{
		Visibility:  "private",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		repos, resp, err := client.Repositories.List(ctx, "", opts)
		if err != nil {
			return nil, err
		}

		for _, repo := range repos {
			repoName := repo.GetName()
			repoOwner := repo.GetOwner().GetLogin()

			// Filter by repository name pattern if provided
			if pattern != "" && !strings.Contains(strings.ToLower(repoName), pattern) {
				continue
			}

			// Get the latest release for the repository
			release, _, err := client.Repositories.GetLatestRelease(ctx, repoOwner, repoName)
			if err != nil {
				// This often returns 404 if no releases exist. We can safely ignore it.
				continue
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

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
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

	// 4. Make the file executable (chmod +x)
	// 0755 is rwxr-xr-x
	if err := os.Chmod(c.AssetName, 0755); err != nil {
		return fmt.Errorf("could not make file executable: %w", err)
	}

	return nil
}
