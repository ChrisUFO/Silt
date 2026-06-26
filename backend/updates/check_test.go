package updates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newReleaseServer returns an httptest.Server that responds to
// /repos/{owner}/{repo}/releases/latest with the given release JSON body (and
// status for non-200 cases).
func newReleaseServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestCheckForUpdates_HasUpdate(t *testing.T) {
	const release = `{
		"tag_name": "v0.5.0",
		"html_url": "https://github.com/Chelydra-Labs/Silt/releases/v0.5.0",
		"body": "## New\n- in-app updates",
		"prerelease": false,
		"assets": [
			{"name": "Silt-v0.5.0-windows-installer.exe", "browser_download_url": "https://example/win.exe", "size": 100},
			{"name": "Silt-0.5.0-linux-x86_64.AppImage", "browser_download_url": "https://example/linux.AppImage", "size": 200}
		]
	}`
	srv := newReleaseServer(t, 200, release)
	defer srv.Close()

	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, Repo: "Chelydra-Labs/Silt", AppVersion: "0.4.0"}
	info, err := c.CheckForUpdates(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdates: %v", err)
	}
	if !info.HasUpdate {
		t.Fatal("expected HasUpdate=true for 0.4.0 < 0.5.0")
	}
	if info.LatestVersion != "0.5.0" {
		t.Errorf("LatestVersion = %q, want 0.5.0 (v-prefix stripped)", info.LatestVersion)
	}
	if info.ReleaseNotes == "" {
		t.Error("expected non-empty release notes")
	}
	if info.Asset == nil {
		t.Error("expected a platform-matching asset")
	}
}

func TestCheckForUpdates_UpToDate(t *testing.T) {
	srv := newReleaseServer(t, 200, `{"tag_name":"v0.4.0","html_url":"u","body":"","assets":[]}`)
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.4.0"}
	info, err := c.CheckForUpdates(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdates: %v", err)
	}
	if info.HasUpdate {
		t.Fatal("expected HasUpdate=false for equal versions")
	}
	if info.Asset != nil {
		t.Errorf("expected nil asset for empty asset list, got %+v", info.Asset)
	}
}

func TestCheckForUpdates_OlderCurrent(t *testing.T) {
	// Running 0.6.0 vs latest 0.5.0 → no update.
	srv := newReleaseServer(t, 200, `{"tag_name":"v0.5.0","html_url":"u","body":"","assets":[]}`)
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.6.0"}
	info, err := c.CheckForUpdates(context.Background())
	if err != nil {
		t.Fatalf("CheckForUpdates: %v", err)
	}
	if info.HasUpdate {
		t.Fatal("expected HasUpdate=false when current is newer than latest")
	}
}

func TestCheckForUpdates_Non2xxIsQuietError(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		srv := newReleaseServer(t, status, `{}`)
		c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.4.0"}
		_, err := c.CheckForUpdates(context.Background())
		if err == nil {
			srv.Close()
			t.Fatalf("status %d: expected error, got nil", status)
		}
		if !errors.Is(err, ErrUpdateCheckFailed) {
			srv.Close()
			t.Errorf("status %d: expected ErrUpdateCheckFailed, got %v", status, err)
		}
		srv.Close()
	}
}

func TestCheckForUpdates_MalformedJSON(t *testing.T) {
	srv := newReleaseServer(t, 200, `{not valid json`)
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.4.0"}
	_, err := c.CheckForUpdates(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !errors.Is(err, ErrUpdateCheckFailed) {
		t.Errorf("expected ErrUpdateCheckFailed, got %v", err)
	}
}

// TestCheckForUpdates_NoTokenInRequest asserts the read path ships no
// credential (#312 AC7): the request carries no Authorization header.
func TestCheckForUpdates_NoTokenInRequest(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.5.0","html_url":"u","assets":[]}`))
	}))
	defer srv.Close()
	c := &Client{HTTPClient: srv.Client(), APIBase: srv.URL, AppVersion: "0.4.0"}
	if _, err := c.CheckForUpdates(context.Background()); err != nil {
		t.Fatalf("CheckForUpdates: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q (a token must not be embedded)", gotAuth)
	}
}

// Ensure the release struct still decodes the canonical example from the
// GitHub docs (forward-compat guard against a field rename).
func TestGithubReleaseDecode(t *testing.T) {
	raw := `{"tag_name":"v1.0.0","html_url":"h","body":"b","prerelease":false,"assets":[{"name":"a","browser_download_url":"u","size":7}]}`
	var r githubRelease
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.TagName != "v1.0.0" || len(r.Assets) != 1 || r.Assets[0].Size != 7 {
		t.Fatalf("unexpected decode: %+v", r)
	}
}
