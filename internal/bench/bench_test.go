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

func TestSequentialPlanIsDeterministicAndContiguous(t *testing.T) {
	plan, err := PlanPattern(64*1024, 4096, 6, 3, PatternSequential)
	if err != nil {
		t.Fatal(err)
	}
	want := []Range{
		{Offset: 3 * 4096, Length: 4096},
		{Offset: 4 * 4096, Length: 4096},
		{Offset: 5 * 4096, Length: 4096},
		{Offset: 6 * 4096, Length: 4096},
		{Offset: 7 * 4096, Length: 4096},
		{Offset: 8 * 4096, Length: 4096},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("sequential plan=%+v want %+v", plan, want)
	}
}

func TestClusteredPlanGroupsSequentialReads(t *testing.T) {
	a, err := PlanPattern(1<<20, 4096, 18, 42, PatternClustered)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PlanPattern(1<<20, 4096, 18, 42, PatternClustered)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("clustered plan is not deterministic")
	}
	for cluster := 0; cluster < len(a); cluster += 8 {
		end := cluster + 8
		if end > len(a) {
			end = len(a)
		}
		for i := cluster + 1; i < end; i++ {
			slots := (1 << 20) / 4096
			prev := a[i-1].Offset / 4096
			current := a[i].Offset / 4096
			if current != (prev+1)%int64(slots) {
				t.Fatalf("cluster is not sequential at %d: %+v", i, a[cluster:end])
			}
		}
	}
}

func TestPlanPatternRejectsUnknownMode(t *testing.T) {
	if _, err := PlanPattern(4096, 512, 4, 1, Pattern("bursty")); err == nil {
		t.Fatal("accepted unsupported benchmark pattern")
	}
}

func TestPlanFileRoundTrip(t *testing.T) {
	original := PlanFile{
		SchemaVersion: CurrentPlanSchema,
		FileSize:      65536,
		ETag:          "\"fixture-v1\"",
		ReadSize:      4096,
		Seed:          42,
		Pattern:       PatternClustered,
		Ranges: []Range{
			{Offset: 4096, Length: 4096},
			{Offset: 8192, Length: 4096},
		},
	}
	var encoded bytes.Buffer
	if err := SavePlan(&encoded, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPlan(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Fatalf("loaded plan=%+v want %+v", loaded, original)
	}
}

func TestPlanTargetBindsCurrentSchemaToETag(t *testing.T) {
	plan := PlanFile{
		SchemaVersion: CurrentPlanSchema,
		FileSize:      4096,
		ETag:          "\"fixture-v1\"",
		ReadSize:      512,
		Seed:          1,
		Pattern:       PatternRandom,
		Ranges:        []Range{{Offset: 0, Length: 512}},
	}
	if err := ValidatePlanFile(plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanTarget(plan, 4096, "\"fixture-v1\""); err != nil {
		t.Fatalf("matching target rejected: %v", err)
	}
	if err := ValidatePlanTarget(plan, 4096, "\"fixture-v2\""); err == nil {
		t.Fatal("accepted same-size target with different ETag")
	}
	if err := ValidatePlanTarget(plan, 8192, "\"fixture-v1\""); err == nil {
		t.Fatal("accepted target with different size")
	}
}

func TestLegacyPlanRemainsSizeBoundForNormalization(t *testing.T) {
	plan := PlanFile{
		SchemaVersion: 1,
		FileSize:      4096,
		ReadSize:      512,
		Seed:          1,
		Pattern:       PatternRandom,
		Ranges:        []Range{{Offset: 0, Length: 512}},
	}
	if err := ValidatePlanFile(plan); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanTarget(plan, 4096, "\"different-etag\""); err != nil {
		t.Fatalf("legacy plan should remain size-bound: %v", err)
	}
}

func TestCurrentPlanSchemaRequiresNormalizedETag(t *testing.T) {
	base := PlanFile{
		SchemaVersion: CurrentPlanSchema,
		FileSize:      4096,
		ReadSize:      512,
		Seed:          1,
		Pattern:       PatternRandom,
		Ranges:        []Range{{Offset: 0, Length: 512}},
	}
	for _, etag := range []string{"", "  ", " \"fixture\"", "\"fixture\"\n"} {
		plan := base
		plan.ETag = etag
		if err := ValidatePlanFile(plan); err == nil {
			t.Fatalf("accepted invalid ETag %q", etag)
		}
	}
}

func TestPlanFileRejectsInvalidInput(t *testing.T) {
	invalid := PlanFile{
		SchemaVersion: 1,
		FileSize:      4096,
		ReadSize:      512,
		Seed:          1,
		Pattern:       PatternRandom,
		Ranges:        []Range{{Offset: 4000, Length: 512}},
	}
	if err := ValidatePlanFile(invalid); err == nil {
		t.Fatal("accepted out-of-bounds saved plan")
	}

	_, err := LoadPlan(strings.NewReader(
		`{"schema_version":1,"file_size":4096,"read_size":512,"seed":1,"pattern":"random","ranges":[{"offset":0,"length":512}],"unexpected":true}`,
	))
	if err == nil {
		t.Fatal("accepted unknown saved-plan field")
	}
}

func TestLoadPlanRejectsTrailingJSONAndGarbage(t *testing.T) {
	base := `{"schema_version":1,"file_size":4096,"read_size":512,"seed":1,"pattern":"random","ranges":[{"offset":0,"length":512}]}`

	for _, tc := range []struct {
		name   string
		suffix string
	}{
		{name: "second-json-value", suffix: "\n{}"},
		{name: "trailing-garbage", suffix: "\nnot-json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadPlan(strings.NewReader(base + tc.suffix)); err == nil {
				t.Fatalf("accepted saved plan with %s", tc.name)
			}
		})
	}
}

func TestLoadPlanRejectsPayloadLargerThanLimit(t *testing.T) {
	base := `{"schema_version":1,"file_size":4096,"read_size":512,"seed":1,"pattern":"random","ranges":[{"offset":0,"length":512}]}`
	padding := strings.Repeat(" ", int(maxPlanFileBytes)+1)
	if _, err := LoadPlan(strings.NewReader(base + padding)); err == nil {
		t.Fatal("accepted saved plan larger than 16 MiB")
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
		if r.Header.Get("Accept-Encoding") != "identity" {
			http.Error(w, "identity encoding required", http.StatusBadRequest)
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
	if stats.Reads != len(plan) || stats.Requests != len(plan) || stats.Bytes != int64(len(plan))*4096 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.FreshConnections+stats.ReusedConnections != stats.Requests {
		t.Fatalf("connection accounting does not match requests: %+v", stats)
	}
	if stats.FreshConnections < 1 {
		t.Fatalf("benchmark did not observe an initial connection: %+v", stats)
	}
}

func TestHTTPRangeMeasurementRejectsTransformedResponse(t *testing.T) {
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
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Range", "bytes 0-63/4096")
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
	if size != int64(len(data)) {
		t.Fatalf("unexpected probe size: %d", size)
	}
	_, err = MeasureHTTP(
		context.Background(),
		server.Client(),
		server.URL,
		"fixture",
		gotETag,
		[]Range{{Offset: 0, Length: 64}},
		HTTPOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "Content-Encoding") {
		t.Fatalf("transformed range response was not rejected: %v", err)
	}
}

func TestHTTPBatchedRangeMeasurement(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 4096)
	etag := `"fixture-v1"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/fixture" {
			http.NotFound(w, r)
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
	plan, err := PlanPattern(size, 1024, 10, 5, PatternClustered)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := MeasureHTTPBatched(
		context.Background(),
		server.Client(),
		server.URL,
		"fixture",
		gotETag,
		plan,
		4,
		HTTPOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Reads != 10 || stats.Requests != 3 || stats.Bytes != 10*1024 {
		t.Fatalf("unexpected batched stats: %+v", stats)
	}
	if stats.FreshConnections+stats.ReusedConnections != stats.Requests {
		t.Fatalf("batched connection accounting mismatch: %+v", stats)
	}
}

func TestHTTPBatchedRejectsInvalidBatchSize(t *testing.T) {
	plan := []Range{{Offset: 0, Length: 16}}
	client := &http.Client{}
	for _, batch := range []int{0, 17} {
		if _, err := MeasureHTTPBatched(
			context.Background(),
			client,
			"http://127.0.0.1",
			"fixture",
			`"etag"`,
			plan,
			batch,
			HTTPOptions{},
		); err == nil {
			t.Fatalf("accepted invalid batch size %d", batch)
		}
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
	if stats.Reads != 8 || stats.Requests != 8 || stats.Bytes != 8*8192 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestMeasureFileTargetRequiresServerSizeMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x5a}, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := []Range{{Offset: 0, Length: 4096}}

	stats, err := MeasureFileTarget(path, plan, 8192)
	if err != nil {
		t.Fatalf("matching baseline rejected: %v", err)
	}
	if stats.Bytes != 4096 || stats.Reads != 1 {
		t.Fatalf("unexpected matching stats: %+v", stats)
	}

	if _, err := MeasureFileTarget(path, plan, 16384); err == nil ||
		!strings.Contains(err.Error(), "does not match server file size") {
		t.Fatalf("mismatched baseline size was not rejected: %v", err)
	}
	if _, err := MeasureFileTarget(path, plan, -1); err == nil {
		t.Fatal("negative expected baseline size was accepted")
	}
}

func TestVerifyBaselineSamplesMatchesAndDetectsDifference(t *testing.T) {
	data := make([]byte, 16*1024)
	for i := range data {
		data[i] = byte((i * 29) & 0xff)
	}
	etag := `"fixture-v1"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	plan := []Range{
		{Offset: 0, Length: 1024},
		{Offset: 4096, Length: 1024},
		{Offset: 8192, Length: 1024},
		{Offset: 12 * 1024, Length: 1024},
	}

	if err := VerifyBaselineSamples(
		context.Background(), server.Client(), server.URL, "fixture", etag, path,
		plan, int64(len(data)), 3, HTTPOptions{},
	); err != nil {
		t.Fatalf("matching sampled baseline rejected: %v", err)
	}

	mutated := append([]byte(nil), data...)
	mutated[plan[1].Offset+17] ^= 0xff
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBaselineSamples(
		context.Background(), server.Client(), server.URL, "fixture", etag, path,
		plan, int64(len(data)), 3, HTTPOptions{},
	); err == nil || !strings.Contains(err.Error(), "baseline differs") {
		t.Fatalf("same-size mismatched baseline was not rejected: %v", err)
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
