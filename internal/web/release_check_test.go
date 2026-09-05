package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseSemver(t *testing.T) {
	cases := []struct {
		raw                 string
		major, minor, patch int
		ok                  bool
	}{
		{"v1.2.3", 1, 2, 3, true},
		{"1.2.3", 1, 2, 3, true},
		{"v0.7.0", 0, 7, 0, true},
		{"dev", 0, 0, 0, false},
		{"", 0, 0, 0, false},
		{"v1.2", 0, 0, 0, false},
		{"v1.2.3.4", 0, 0, 0, false},
		{"v1.2.x", 0, 0, 0, false},
	}
	for _, c := range cases {
		major, minor, patch, ok := parseSemver(c.raw)
		if ok != c.ok || (ok && (major != c.major || minor != c.minor || patch != c.patch)) {
			t.Errorf("parseSemver(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)", c.raw, major, minor, patch, ok, c.major, c.minor, c.patch, c.ok)
		}
	}
}

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.8.0", "v0.7.0", true},
		{"v1.0.0", "v0.7.0", true},
		{"v0.7.1", "v0.7.0", true},
		{"v0.7.0", "v0.7.0", false},
		{"v0.6.0", "v0.7.0", false},
		{"v0.8.0", "dev", false},
		{"garbage", "v0.7.0", false},
		{"v0.8.0", "", false},
	}
	for _, c := range cases {
		if got := isNewerVersion(c.latest, c.current); got != c.want {
			t.Errorf("isNewerVersion(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestFetchLatestReleaseFrom(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"tag_name":"v0.8.0","name":"v0.8.0"}`))
		}))
		defer srv.Close()

		tag, err := fetchLatestReleaseFrom(srv.URL, srv.Client())
		if err != nil {
			t.Fatalf("fetchLatestReleaseFrom: %v", err)
		}
		if tag != "v0.8.0" {
			t.Errorf("tag = %q, want v0.8.0", tag)
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if _, err := fetchLatestReleaseFrom(srv.URL, srv.Client()); err == nil {
			t.Error("want error for 404, got nil")
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("not json"))
		}))
		defer srv.Close()

		if _, err := fetchLatestReleaseFrom(srv.URL, srv.Client()); err == nil {
			t.Error("want error for malformed JSON, got nil")
		}
	})
}
