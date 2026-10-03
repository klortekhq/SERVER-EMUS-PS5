package bench

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPlanDeterministicAndBounded(t *testing.T) {
	a, err := Plan(1<<20, 4096, 64, 12345)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Plan(1<<20, 4096, 64, 12345)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different plans")
	}
	for _, item := range a {
		if item.Offset < 0 || item.Length != 4096 || item.Offset+item.Length > 1<<20 {
			t.Fatalf("out-of-bounds range: %+v", item)
		}
	}
}

func TestHTTPRangeMeasurement(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	etag := `"fixture-v1"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/fixture" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("If-Match") != etag {
			http.Error(w, "precondition failed", http.StatusPreconditionFailed)
			return
		}
		http.ServeContent(w, r, "fixture.bin", time.Unix(1, 0), bytes.NewReader(data))
	}))
	defer server.Close()

	options := HTTPOptions{BearerToken: "fixture-secret"}
	size, gotETag, err := ProbeHTTP(context.Background(), server.Client(), server.URL, "fixture", options)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(data)) || gotETag != etag {
		t.Fatalf("unexpected probe: size=%d etag=%q", size, gotETag)
	}
	plan, err := Plan(size, 4096, 12, 7)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := MeasureHTTP(context.Background(), server.Client(), server.URL, "fixture", gotETag, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Requests != len(plan) || stats.Bytes != int64(len(plan))*4096 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestHTTPOptionsRejectLineBreak(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyHTTPOptions(req, HTTPOptions{BearerToken: "bad\nvalue"}); err == nil {
		t.Fatal("accepted bearer token containing line break")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("authorization header set after rejected token: %q", got)
	}
}

func TestMeasureFileUsesSamePlan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x5a}, 128*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(128*1024, 8192, 8, 99)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := MeasureFile(path, plan)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Requests != 8 || stats.Bytes != 8*8192 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestPlanRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		size     int64
		readSize int64
		count    int
	}{
		{0, 1, 1},
		{1, 0, 1},
		{1, 1, 0},
	} {
		if _, err := Plan(tc.size, tc.readSize, tc.count, 1); err == nil {
			t.Fatalf("accepted invalid input: %+v", tc)
		}
	}
}

func TestValidateContentRangeStrict(t *testing.T) {
	item := Range{Offset: 100, Length: 64}
	for _, tc := range []struct {
		name   string
		header string
		ok     bool
	}{
		{name: "numeric-total", header: "bytes 100-163/4096", ok: true},
		{name: "unknown-total", header: "bytes 100-163/*", ok: true},
		{name: "wrong-start", header: "bytes 99-162/4096"},
		{name: "wrong-end", header: "bytes 100-164/4096"},
		{name: "too-small-total", header: "bytes 100-163/163"},
		{name: "wrong-unit", header: "items 100-163/4096"},
		{name: "missing-total", header: "bytes 100-163"},
		{name: "non-numeric", header: "bytes nope-163/4096"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateContentRange(tc.header, item)
			if tc.ok && err != nil {
				t.Fatalf("valid Content-Range rejected: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("invalid Content-Range accepted: %q", tc.header)
			}
		})
	}
}

func TestHTTPRangeMeasurementRejectsWrongContentRange(t *testing.T) {
	data := bytes.Repeat([]byte{0x5a}, 4096)
	etag := `"fixture-v1"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Range", "bytes 1-64/4096")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[:64])
	}))
	defer server.Close()

	size, gotETag, err := ProbeHTTP(
		context.Background(),
		server.Client(),
		server.URL,
		"fixture",
		HTTPOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan := []Range{{Offset: 0, Length: 64}}
	if _, err := MeasureHTTP(
		context.Background(),
		server.Client(),
		server.URL,
		"fixture",
		gotETag,
		plan,
		HTTPOptions{},
	); err == nil || !strings.Contains(err.Error(), "Content-Range") {
		t.Fatalf("wrong Content-Range was not rejected: %v", err)
	}
	if size != int64(len(data)) {
		t.Fatalf("unexpected probe size: %d", size)
	}
}
