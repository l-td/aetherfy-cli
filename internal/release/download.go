package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
)

// releaseTagPath matches the path GitHub redirects /releases/latest to.
var releaseTagPath = regexp.MustCompile(`/releases/tag/([^/?#]+)$`)

// ResolveLatestTag returns the newest published tag, e.g. "v0.1.0".
//
// github.com/<repo>/releases/latest redirects to /releases/tag/<tag>, so the
// answer is the final URL — see LatestURL for why this route and not the API.
// The response body is never parsed; only where the request ended up matters.
func ResolveLatestTag(ctx context.Context, client *http.Client) (string, error) {
	target := LatestURL()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", target, err)
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the connection can be reused; the body is not
	// the answer.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	final := resp.Request.URL.String()
	if tag, ok := tagFromReleaseURL(final); ok {
		return tag, nil
	}

	return "", fmt.Errorf("could not work out the latest release: %s answered HTTP %d and ended at %s, "+
		"which is not a /releases/tag/<tag> URL. The most likely reason is that no release has been "+
		"published yet — check https://github.com/%s/releases",
		target, resp.StatusCode, final, Repo)
}

// tagFromReleaseURL reads the tag out of a .../releases/tag/<tag> URL.
func tagFromReleaseURL(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	m := releaseTagPath.FindStringSubmatch(parsed.Path)
	if m == nil {
		return "", false
	}
	tag, err := url.PathUnescape(m[1])
	if err != nil || tag == "" {
		return "", false
	}
	return tag, true
}

// Download fetches from into the file at dest.
//
// A non-200 is an error naming the URL. scripts/install.sh learned the same
// lesson the hard way: a download that fails silently — wrong tag, renamed
// asset, release not published yet — is the likeliest failure there is, and
// the URL it tried is the only thing that identifies which one happened.
func Download(ctx context.Context, client *http.Client, from, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, from, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %s: %w", from, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s returned HTTP %d", from, resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(dest)
		return fmt.Errorf("could not save %s: %w", from, err)
	}
	return nil
}
