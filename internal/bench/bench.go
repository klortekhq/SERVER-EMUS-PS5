package bench

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type Range struct {
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

type Stats struct {
	Source       string  `json:"source"`
	Requests     int     `json:"requests"`
	Bytes        int64   `json:"bytes"`
	ElapsedMS    float64 `json:"elapsed_ms"`
	MiBPerSecond float64 `json:"mib_per_second"`
	P50MS        float64 `json:"p50_ms"`
	P95MS        float64 `json:"p95_ms"`
	MaxMS        float64 `json:"max_ms"`
}

func Plan(size, readSize int64, count int, seed uint64) ([]Range, error) {
	if size <= 0 || readSize <= 0 || count <= 0 {
		return nil, errors.New("size, readSize and count must be positive")
	}
	if readSize > size {
		readSize = size
	}
	maxOffset := size - readSize
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	out := make([]Range, count)
	state := seed
	for i := range out {
		state ^= state >> 12
		state ^= state << 25
		state ^= state >> 27
		value := state * 0x2545F4914F6CDD1D
		offset := int64(0)
		if maxOffset > 0 {
			offset = int64(value % uint64(maxOffset+1))
		}
		out[i] = Range{Offset: offset, Length: readSize}
	}
	return out, nil
}

func ProbeHTTP(ctx context.Context, client *http.Client, baseURL, fileID string) (int64, string, error) {
	endpoint, err := fileURL(baseURL, fileID)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, endpoint, nil)
	if err != nil {
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

func MeasureHTTP(ctx context.Context, client *http.Client, baseURL, fileID, etag string, plan []Range) (Stats, error) {
	endpoint, err := fileURL(baseURL, fileID)
	if err != nil {
		return Stats{}, err
	}
	if len(plan) == 0 {
		return Stats{}, errors.New("plan is empty")
	}
	var total int64
	latencies := make([]time.Duration, 0, len(plan))
	start := time.Now()
	for _, item := range plan {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return Stats{}, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", item.Offset, item.Offset+item.Length-1))
		req.Header.Set("If-Match", etag)
		requestStart := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return Stats{}, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, item.Length+1))
		closeErr := resp.Body.Close()
		latencies = append(latencies, time.Since(requestStart))
		if readErr != nil {
			return Stats{}, readErr
		}
		if closeErr != nil {
			return Stats{}, closeErr
		}
		if resp.StatusCode != http.StatusPartialContent {
			return Stats{}, fmt.Errorf("range GET returned %s", resp.Status)
		}
		if int64(len(body)) != item.Length {
			return Stats{}, fmt.Errorf("range GET returned %d bytes, expected %d", len(body), item.Length)
		}
		total += int64(len(body))
	}
	return summarize("emus-http-range", total, time.Since(start), latencies), nil
}

func MeasureFile(path string, plan []Range) (Stats, error) {
	if len(plan) == 0 {
		return Stats{}, errors.New("plan is empty")
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
	return summarize("mounted-file", total, time.Since(start), latencies), nil
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

func summarize(source string, total int64, elapsed time.Duration, latencies []time.Duration) Stats {
	seconds := elapsed.Seconds()
	rate := 0.0
	if seconds > 0 {
		rate = (float64(total) / (1024 * 1024)) / seconds
	}
	return Stats{
		Source:       source,
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
