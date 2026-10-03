package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Range struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

const CurrentPlanSchema = 2

type PlanFile struct {
	SchemaVersion int     `json:"schema_version"`
	FileSize      int64   `json:"file_size"`
	ETag          string  `json:"etag,omitempty"`
	ReadSize      int64   `json:"read_size"`
	Seed          uint64  `json:"seed"`
	Pattern       Pattern `json:"pattern"`
	Ranges        []Range `json:"ranges"`
}

func SavePlan(w io.Writer, plan PlanFile) error {
	if err := ValidatePlanFile(plan); err != nil {
		return err
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

const maxPlanFileBytes int64 = 16 << 20

func LoadPlan(r io.Reader) (PlanFile, error) {
	payload, err := io.ReadAll(io.LimitReader(r, maxPlanFileBytes+1))
	if err != nil {
		return PlanFile{}, err
	}
	if int64(len(payload)) > maxPlanFileBytes {
		return PlanFile{}, fmt.Errorf("saved plan exceeds %d-byte limit", maxPlanFileBytes)
	}

	var plan PlanFile
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return PlanFile{}, err
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return PlanFile{}, errors.New("saved plan contains trailing JSON value")
		}
		return PlanFile{}, fmt.Errorf("saved plan contains trailing data: %w", err)
	}

	if err := ValidatePlanFile(plan); err != nil {
		return PlanFile{}, err
	}
	return plan, nil
}

func ValidatePlanFile(plan PlanFile) error {
	switch plan.SchemaVersion {
	case 1:
		// Legacy size-bound plans remain readable so they can be normalized by
		// re-saving them through the current CLI.
	case CurrentPlanSchema:
		if strings.TrimSpace(plan.ETag) == "" {
			return errors.New("plan etag is required by schema 2")
		}
		if strings.TrimSpace(plan.ETag) != plan.ETag ||
			strings.ContainsAny(plan.ETag, "\r\n") {
			return errors.New("plan etag is not normalized")
		}
	default:
		return fmt.Errorf("unsupported plan schema %d", plan.SchemaVersion)
	}
	if plan.FileSize <= 0 || plan.ReadSize <= 0 {
		return errors.New("plan file_size and read_size must be positive")
	}
	if len(plan.Ranges) == 0 {
		return errors.New("plan ranges are empty")
	}
	switch plan.Pattern {
	case PatternRandom, PatternSequential, PatternClustered:
	default:
		return fmt.Errorf("unsupported benchmark pattern %q", plan.Pattern)
	}
	for i, item := range plan.Ranges {
		if item.Offset < 0 || item.Length <= 0 ||
			item.Offset > plan.FileSize ||
			item.Length > plan.FileSize-item.Offset {
			return fmt.Errorf("plan range %d is outside file bounds", i)
		}
	}
	return nil
}

func ValidatePlanTarget(plan PlanFile, fileSize int64, etag string) error {
	if plan.FileSize != fileSize {
		return fmt.Errorf(
			"plan file size %d does not match server file size %d",
			plan.FileSize,
			fileSize,
		)
	}
	if plan.SchemaVersion >= CurrentPlanSchema && plan.ETag != strings.TrimSpace(etag) {
		return fmt.Errorf(
			"plan etag %q does not match server etag %q",
			plan.ETag,
			strings.TrimSpace(etag),
		)
	}
	return nil
}

type Stats struct {
	Source       string  `json:"source"`
	Reads        int     `json:"reads"`
	Requests     int     `json:"requests"`
	Bytes        int64   `json:"bytes"`
	ElapsedMS    float64 `json:"elapsed_ms"`
	MiBPerSecond float64 `json:"mib_per_second"`
	P50MS        float64 `json:"p50_ms"`
	P95MS        float64 `json:"p95_ms"`
	MaxMS             float64 `json:"max_ms"`
	FreshConnections  int     `json:"fresh_connections,omitempty"`
	ReusedConnections int     `json:"reused_connections,omitempty"`
}

type HTTPOptions struct {
	BearerToken string
}

type Pattern string

const (
	PatternRandom     Pattern = "random"
	PatternSequential Pattern = "sequential"
	PatternClustered  Pattern = "clustered"
)

// Plan preserves the original deterministic random-read behavior.
func Plan(size, readSize int64, count int, seed uint64) ([]Range, error) {
	return PlanPattern(size, readSize, count, seed, PatternRandom)
}

func PlanPattern(size, readSize int64, count int, seed uint64, pattern Pattern) ([]Range, error) {
	if size <= 0 || readSize <= 0 || count <= 0 {
		return nil, errors.New("size, readSize and count must be positive")
	}
	if readSize > size {
		readSize = size
	}
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}

	switch pattern {
	case PatternRandom:
		return randomPlan(size, readSize, count, seed), nil
	case PatternSequential:
		return sequentialPlan(size, readSize, count, seed), nil
	case PatternClustered:
		return clusteredPlan(size, readSize, count, seed), nil
	default:
		return nil, fmt.Errorf("unsupported benchmark pattern %q", pattern)
	}
}

func randomPlan(size, readSize int64, count int, seed uint64) []Range {
	maxOffset := size - readSize
	out := make([]Range, count)
	state := seed
	for i := range out {
		value := nextRandom(&state)
		offset := int64(0)
		if maxOffset > 0 {
			offset = int64(value % uint64(maxOffset+1))
		}
		out[i] = Range{Offset: offset, Length: readSize}
	}
	return out
}

func sequentialPlan(size, readSize int64, count int, seed uint64) []Range {
	slotCount := size / readSize
	if slotCount == 0 {
		slotCount = 1
	}
	startSlot := int64(seed % uint64(slotCount))
	out := make([]Range, count)
	for i := range out {
		slot := (startSlot + int64(i)) % slotCount
		out[i] = Range{Offset: slot * readSize, Length: readSize}
	}
	return out
}

func clusteredPlan(size, readSize int64, count int, seed uint64) []Range {
	const clusterReads = 8
	slotCount := size / readSize
	if slotCount == 0 {
		slotCount = 1
	}
	out := make([]Range, count)
	state := seed
	for base := 0; base < count; base += clusterReads {
		startSlot := int64(nextRandom(&state) % uint64(slotCount))
		limit := clusterReads
		if remaining := count - base; remaining < limit {
			limit = remaining
		}
		for i := 0; i < limit; i++ {
			slot := (startSlot + int64(i)) % slotCount
			out[base+i] = Range{Offset: slot * readSize, Length: readSize}
		}
	}
	return out
}

func nextRandom(state *uint64) uint64 {
	*state ^= *state >> 12
	*state ^= *state << 25
	*state ^= *state >> 27
	return *state * 0x2545F4914F6CDD1D
}

func ProbeHTTP(ctx context.Context, client *http.Client, baseURL, fileID string, options HTTPOptions) (int64, string, error) {
	endpoint, err := fileURL(baseURL, fileID)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if err := applyHTTPOptions(req, options); err != nil {
		return 0, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("HEAD returned %s", resp.Status)
	}
	if err := validateIdentityEncoding(resp.Header); err != nil {
		return 0, "", fmt.Errorf("HEAD: %w", err)
	}
	if resp.ContentLength <= 0 {
		return 0, "", errors.New("HEAD returned invalid Content-Length")
	}
	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	if etag == "" {
		return 0, "", errors.New("HEAD returned no ETag")
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Accept-Ranges")), "bytes") {
		return 0, "", errors.New("server did not advertise byte ranges")
	}
	return resp.ContentLength, etag, nil
}

func MeasureHTTP(ctx context.Context, client *http.Client, baseURL, fileID, etag string, plan []Range, options HTTPOptions) (Stats, error) {
	return MeasureHTTPBatched(ctx, client, baseURL, fileID, etag, plan, 1, options)
}

func MeasureHTTPBatched(ctx context.Context, client *http.Client, baseURL, fileID, etag string, plan []Range, batchSize int, options HTTPOptions) (Stats, error) {
	endpoint, err := fileURL(baseURL, fileID)
	if err != nil {
		return Stats{}, err
	}
	if len(plan) == 0 {
		return Stats{}, errors.New("plan is empty")
	}
	if batchSize < 1 || batchSize > 16 {
		return Stats{}, errors.New("batch size must be between 1 and 16")
	}

	var total int64
	var freshConnections int
	var reusedConnections int
	latencies := make([]time.Duration, 0, (len(plan)+batchSize-1)/batchSize)
	started := time.Now()

	for begin := 0; begin < len(plan); begin += batchSize {
		endIndex := begin + batchSize
		if endIndex > len(plan) {
			endIndex = len(plan)
		}
		batch := plan[begin:endIndex]

		rangeValues := make([]string, len(batch))
		for i, item := range batch {
			if item.Offset < 0 || item.Length <= 0 {
				return Stats{}, errors.New("plan contains an invalid HTTP range")
			}
			rangeEnd := item.Offset + item.Length - 1
			if rangeEnd < item.Offset {
				return Stats{}, errors.New("plan HTTP range overflows int64")
			}
			rangeValues[i] = fmt.Sprintf("%d-%d", item.Offset, rangeEnd)
		}

		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				if info.Reused {
					reusedConnections++
				} else {
					freshConnections++
				}
			},
		}
		requestContext := httptrace.WithClientTrace(ctx, trace)
		req, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint, nil)
		if err != nil {
			return Stats{}, err
		}
		req.Header.Set("Range", "bytes="+strings.Join(rangeValues, ","))
		req.Header.Set("If-Match", etag)
		req.Header.Set("Accept-Encoding", "identity")
		if err := applyHTTPOptions(req, options); err != nil {
			return Stats{}, err
		}

		requestStart := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return Stats{}, err
		}
		if resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			return Stats{}, fmt.Errorf("range GET returned %s", resp.Status)
		}
		if got := strings.TrimSpace(resp.Header.Get("ETag")); got != etag {
			_ = resp.Body.Close()
			return Stats{}, fmt.Errorf(
				"range GET ETag changed: got %q, expected %q",
				got,
				etag,
			)
		}
		if err := validateIdentityEncoding(resp.Header); err != nil {
			_ = resp.Body.Close()
			return Stats{}, fmt.Errorf("range GET: %w", err)
		}

		if len(batch) == 1 {
			item := batch[0]
			if err := validateContentRange(resp.Header.Get("Content-Range"), item); err != nil {
				_ = resp.Body.Close()
				return Stats{}, err
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, item.Length+1))
			closeErr := resp.Body.Close()
			if readErr != nil {
				return Stats{}, readErr
			}
			if closeErr != nil {
				return Stats{}, closeErr
			}
			if int64(len(body)) != item.Length {
				return Stats{}, fmt.Errorf(
					"range GET returned %d bytes, expected %d",
					len(body),
					item.Length,
				)
			}
			total += int64(len(body))
		} else {
			mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
			if err != nil || mediaType != "multipart/byteranges" || params["boundary"] == "" {
				_ = resp.Body.Close()
				return Stats{}, fmt.Errorf(
					"multi-range GET returned invalid Content-Type %q",
					resp.Header.Get("Content-Type"),
				)
			}

			reader := multipart.NewReader(resp.Body, params["boundary"])
			for i, item := range batch {
				part, err := reader.NextPart()
				if err != nil {
					_ = resp.Body.Close()
					return Stats{}, fmt.Errorf("multi-range part %d: %w", i, err)
				}
				if err := validateContentRange(part.Header.Get("Content-Range"), item); err != nil {
					_ = part.Close()
					_ = resp.Body.Close()
					return Stats{}, fmt.Errorf("multi-range part %d: %w", i, err)
				}
				if err := validateIdentityEncoding(part.Header); err != nil {
					_ = part.Close()
					_ = resp.Body.Close()
					return Stats{}, fmt.Errorf("multi-range part %d: %w", i, err)
				}
				body, readErr := io.ReadAll(io.LimitReader(part, item.Length+1))
				closeErr := part.Close()
				if readErr != nil {
					_ = resp.Body.Close()
					return Stats{}, readErr
				}
				if closeErr != nil {
					_ = resp.Body.Close()
					return Stats{}, closeErr
				}
				if int64(len(body)) != item.Length {
					_ = resp.Body.Close()
					return Stats{}, fmt.Errorf(
						"multi-range part %d returned %d bytes, expected %d",
						i,
						len(body),
						item.Length,
					)
				}
				total += int64(len(body))
			}
			if extra, err := reader.NextPart(); err != io.EOF {
				if extra != nil {
					_ = extra.Close()
				}
				_ = resp.Body.Close()
				if err == nil {
					return Stats{}, errors.New("multi-range GET returned extra response part")
				}
				return Stats{}, fmt.Errorf("multi-range trailer: %w", err)
			}
			if err := resp.Body.Close(); err != nil {
				return Stats{}, err
			}
		}

		latencies = append(latencies, time.Since(requestStart))
	}

	stats := summarize(
		"emus-http-range",
		total,
		time.Since(started),
		latencies,
		len(plan),
	)
	stats.FreshConnections = freshConnections
	stats.ReusedConnections = reusedConnections
	return stats, nil
}

func MeasureFile(path string, plan []Range) (Stats, error) {
	return MeasureFileTarget(path, plan, 0)
}

func MeasureFileTarget(path string, plan []Range, expectedSize int64) (Stats, error) {
	if len(plan) == 0 {
		return Stats{}, errors.New("plan is empty")
	}
	if expectedSize < 0 {
		return Stats{}, errors.New("expected baseline size cannot be negative")
	}
	f, err := os.Open(path)
	if err != nil {
		return Stats{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Stats{}, err
	}
	if !info.Mode().IsRegular() {
		return Stats{}, errors.New("baseline is not a regular file")
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return Stats{}, fmt.Errorf(
			"baseline file size %d does not match server file size %d",
			info.Size(),
			expectedSize,
		)
	}
	var total int64
	latencies := make([]time.Duration, 0, len(plan))
	start := time.Now()
	for _, item := range plan {
		if item.Offset < 0 || item.Length <= 0 || item.Offset+item.Length > info.Size() {
			return Stats{}, errors.New("plan exceeds baseline file")
		}
		buf := make([]byte, item.Length)
		requestStart := time.Now()
		n, err := f.ReadAt(buf, item.Offset)
		latencies = append(latencies, time.Since(requestStart))
		if err != nil && !errors.Is(err, io.EOF) {
			return Stats{}, err
		}
		if int64(n) != item.Length {
			return Stats{}, fmt.Errorf("baseline read returned %d bytes, expected %d", n, item.Length)
		}
		total += int64(n)
	}
	return summarize("mounted-file", total, time.Since(start), latencies, len(plan)), nil
}

func VerifyBaselineSamples(
	ctx context.Context,
	client *http.Client,
	baseURL, fileID, etag, path string,
	plan []Range,
	expectedSize int64,
	sampleCount int,
	options HTTPOptions,
) error {
	if len(plan) == 0 {
		return errors.New("plan is empty")
	}
	if sampleCount <= 0 {
		return errors.New("baseline verification sample count must be positive")
	}
	if expectedSize <= 0 {
		return errors.New("expected server size must be positive")
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("baseline is not a regular file")
	}
	if info.Size() != expectedSize {
		return fmt.Errorf("baseline file size %d does not match server file size %d", info.Size(), expectedSize)
	}

	endpoint, err := fileURL(baseURL, fileID)
	if err != nil {
		return err
	}

	if sampleCount > len(plan) {
		sampleCount = len(plan)
	}
	indexes := make([]int, sampleCount)
	if sampleCount == 1 {
		indexes[0] = 0
	} else {
		last := len(plan) - 1
		for i := range indexes {
			indexes[i] = i * last / (sampleCount - 1)
		}
	}

	for _, planIndex := range indexes {
		item := plan[planIndex]
		if item.Offset < 0 || item.Length <= 0 || item.Offset > expectedSize || item.Length > expectedSize-item.Offset {
			return fmt.Errorf("verification plan range %d is outside file bounds", planIndex)
		}

		localBytes := make([]byte, item.Length)
		n, readErr := f.ReadAt(localBytes, item.Offset)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if int64(n) != item.Length {
			return fmt.Errorf("baseline verification range %d returned %d bytes, expected %d", planIndex, n, item.Length)
		}

		rangeEnd := item.Offset + item.Length - 1
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", item.Offset, rangeEnd))
		req.Header.Set("If-Match", etag)
		req.Header.Set("Accept-Encoding", "identity")
		if err := applyHTTPOptions(req, options); err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusPartialContent {
			_ = resp.Body.Close()
			return fmt.Errorf("baseline verification range GET returned %s", resp.Status)
		}
		if got := strings.TrimSpace(resp.Header.Get("ETag")); got != etag {
			_ = resp.Body.Close()
			return fmt.Errorf("baseline verification ETag changed: got %q, expected %q", got, etag)
		}
		if err := validateIdentityEncoding(resp.Header); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("baseline verification: %w", err)
		}
		if err := validateContentRange(resp.Header.Get("Content-Range"), item); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("baseline verification range %d: %w", planIndex, err)
		}

		remoteBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, item.Length+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(remoteBytes)) != item.Length {
			return fmt.Errorf("baseline verification range %d returned %d HTTP bytes, expected %d", planIndex, len(remoteBytes), item.Length)
		}
		if !bytes.Equal(remoteBytes, localBytes) {
			return fmt.Errorf("baseline differs from server object at sampled plan range %d (offset=%d length=%d)", planIndex, item.Offset, item.Length)
		}
	}

	return nil
}

func validateIdentityEncoding(header interface{ Get(string) string }) error {
	value := strings.TrimSpace(header.Get("Content-Encoding"))
	if value == "" || strings.EqualFold(value, "identity") {
		return nil
	}
	return fmt.Errorf("unexpected Content-Encoding %q; exact byte benchmarks require identity encoding", value)
}

func validateContentRange(value string, item Range) error {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return fmt.Errorf("range GET returned invalid Content-Range %q", value)
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "bytes "), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("range GET returned invalid Content-Range %q", value)
	}
	bounds := strings.SplitN(parts[0], "-", 2)
	if len(bounds) != 2 {
		return fmt.Errorf("range GET returned invalid Content-Range %q", value)
	}
	start, err := strconv.ParseInt(bounds[0], 10, 64)
	if err != nil {
		return fmt.Errorf("range GET returned invalid Content-Range %q", value)
	}
	end, err := strconv.ParseInt(bounds[1], 10, 64)
	if err != nil {
		return fmt.Errorf("range GET returned invalid Content-Range %q", value)
	}
	expectedEnd := item.Offset + item.Length - 1
	if item.Offset < 0 || item.Length <= 0 || expectedEnd < item.Offset {
		return errors.New("plan contains an invalid HTTP range")
	}
	if start != item.Offset || end != expectedEnd {
		return fmt.Errorf(
			"range GET returned Content-Range %d-%d, expected %d-%d",
			start,
			end,
			item.Offset,
			expectedEnd,
		)
	}
	if parts[1] != "*" {
		total, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || total <= end {
			return fmt.Errorf("range GET returned invalid Content-Range %q", value)
		}
	}
	return nil
}

func applyHTTPOptions(req *http.Request, options HTTPOptions) error {
	if strings.ContainsAny(options.BearerToken, "\r\n") {
		return errors.New("bearer token contains a line break")
	}
	token := strings.TrimSpace(options.BearerToken)
	if token == "" {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func fileURL(baseURL, fileID string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || fileID == "" {
		return "", errors.New("server URL and file ID are required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("server URL must be absolute")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/v1/files/" + url.PathEscape(fileID)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func summarize(source string, total int64, elapsed time.Duration, latencies []time.Duration, reads int) Stats {
	seconds := elapsed.Seconds()
	rate := 0.0
	if seconds > 0 {
		rate = (float64(total) / (1024 * 1024)) / seconds
	}
	return Stats{
		Source:       source,
		Reads:        reads,
		Requests:     len(latencies),
		Bytes:        total,
		ElapsedMS:    millis(elapsed),
		MiBPerSecond: rate,
		P50MS:        millis(percentile(latencies, 0.50)),
		P95MS:        millis(percentile(latencies, 0.95)),
		MaxMS:        millis(percentile(latencies, 1.00)),
	}
}

func percentile(values []time.Duration, q float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	values = append([]time.Duration(nil), values...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(float64(len(values)-1)*q + 0.5)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func millis(v time.Duration) float64 {
	return float64(v) / float64(time.Millisecond)
}

var _ = bytes.MinRead
