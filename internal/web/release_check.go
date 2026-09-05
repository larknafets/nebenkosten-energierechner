package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	releaseAPIURL      = "https://api.github.com/repos/larknafets/nebenkostenrechner/releases/latest"
	releaseCacheTTL    = 6 * time.Hour
	releaseHTTPTimeout = 3 * time.Second
)

type releaseCacheEntry struct {
	latest    string
	checkedAt time.Time
}

var (
	releaseCacheMu sync.Mutex
	releaseCache   releaseCacheEntry
)

// checkForUpdate reports the latest known GitHub release tag and whether
// it's newer than currentVersion. Never blocks the caller: a stale/empty
// cache triggers a background refresh and this returns immediately with
// whatever's cached so far (possibly ""). Silent on any error (offline
// HA-Add-on, GitHub unreachable, unparsable version) - update-checking is
// a nice-to-have, never a reason to slow or break the Dashboard.
func checkForUpdate(currentVersion string) (latest string, available bool) {
	releaseCacheMu.Lock()
	entry := releaseCache
	stale := time.Since(entry.checkedAt) > releaseCacheTTL
	releaseCacheMu.Unlock()

	if stale {
		go refreshReleaseCache()
	}

	if entry.latest == "" {
		return "", false
	}
	return entry.latest, isNewerVersion(entry.latest, currentVersion)
}

func refreshReleaseCache() {
	latest, err := fetchLatestReleaseFrom(releaseAPIURL, &http.Client{Timeout: releaseHTTPTimeout})
	if err != nil {
		return
	}
	releaseCacheMu.Lock()
	releaseCache = releaseCacheEntry{latest: latest, checkedAt: time.Now()}
	releaseCacheMu.Unlock()
}

// fetchLatestReleaseFrom reads a GitHub "releases/latest"-shaped response
// (just the fields this needs) from url via client.
func fetchLatestReleaseFrom(url string, client *http.Client) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("releases/latest: status %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.TagName, nil
}

// parseSemver parses a "vX.Y.Z" tag (leading "v" optional) into its 3
// numeric components. ok=false for anything else - a date-based or
// otherwise non-semver tag never triggers a false "update available".
func parseSemver(v string) (major, minor, patch int, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2], true
}

// isNewerVersion reports whether latest is a strictly greater semver than
// current. false whenever either fails to parse - dev builds ("" or
// "dev"), non-semver tags, or a fetch that returned garbage never claim an
// update exists.
func isNewerVersion(latest, current string) bool {
	lMaj, lMin, lPatch, ok1 := parseSemver(latest)
	cMaj, cMin, cPatch, ok2 := parseSemver(current)
	if !ok1 || !ok2 {
		return false
	}
	if lMaj != cMaj {
		return lMaj > cMaj
	}
	if lMin != cMin {
		return lMin > cMin
	}
	return lPatch > cPatch
}
