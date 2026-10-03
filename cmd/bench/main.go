package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/klortekhq/server-emus-ps5/internal/bench"
)

func main() {
	serverURL := flag.String("server", "", "SERVER-EMUS base URL")
	fileID := flag.String("file-id", "", "catalog file id")
	localPath := flag.String("local", "", "optional mounted-file baseline")
	tokenEnv := flag.String("token-env", "SERVER_EMUS_TOKEN", "environment variable containing bearer token; empty disables auth")
	readSize := flag.Int64("read-size", 64*1024, "bytes per random read")
	samples := flag.Int("samples", 256, "number of random reads")
	seed := flag.Uint64("seed", 1, "deterministic plan seed")
	pattern := flag.String("pattern", "random", "read pattern: random, sequential or clustered")
	batch := flag.Int("batch", 1, "HTTP ranges per request (1-16)")
	planIn := flag.String("plan-in", "", "replay a saved JSON read plan")
	planOut := flag.String("plan-out", "", "write the generated/replayed JSON read plan")
	timeout := flag.Duration("timeout", 2*time.Minute, "whole benchmark timeout")
	jsonOut := flag.Bool("json", false, "emit JSON")
	flag.Parse()

	if *serverURL == "" || *fileID == "" {
		fmt.Fprintln(os.Stderr, "-server and -file-id are required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	options := bench.HTTPOptions{}
	if *tokenEnv != "" {
		options.BearerToken = os.Getenv(*tokenEnv)
	}

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        8,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	size, etag, err := bench.ProbeHTTP(ctx, client, *serverURL, *fileID, options)
	if err != nil {
		fmt.Fprintln(os.Stderr, "probe:", err)
		os.Exit(1)
	}

	var plan []bench.Range
	if *planIn != "" {
		file, openErr := os.Open(*planIn)
		if openErr != nil {
			fmt.Fprintln(os.Stderr, "plan-in:", openErr)
			os.Exit(1)
		}
		loaded, loadErr := bench.LoadPlan(file)
		closeErr := file.Close()
		if loadErr != nil {
			fmt.Fprintln(os.Stderr, "plan-in:", loadErr)
			os.Exit(1)
		}
		if closeErr != nil {
			fmt.Fprintln(os.Stderr, "plan-in close:", closeErr)
			os.Exit(1)
		}
		if targetErr := bench.ValidatePlanTarget(loaded, size, etag); targetErr != nil {
			fmt.Fprintln(os.Stderr, "plan-in:", targetErr)
			os.Exit(1)
		}
		if loaded.SchemaVersion == 1 {
			fmt.Fprintln(
				os.Stderr,
				"plan-in: warning: legacy schema 1 is size-bound only; re-save with -plan-out to bind the current ETag",
			)
		}
		plan = loaded.Ranges
		*readSize = loaded.ReadSize
		*seed = loaded.Seed
		*pattern = string(loaded.Pattern)
	} else {
		var planErr error
		plan, planErr = bench.PlanPattern(
			size,
			*readSize,
			*samples,
			*seed,
			bench.Pattern(*pattern),
		)
		if planErr != nil {
			fmt.Fprintln(os.Stderr, "plan:", planErr)
			os.Exit(1)
		}
	}

	if *planOut != "" {
		file, createErr := os.Create(*planOut)
		if createErr != nil {
			fmt.Fprintln(os.Stderr, "plan-out:", createErr)
			os.Exit(1)
		}
		saveErr := bench.SavePlan(file, bench.PlanFile{
			SchemaVersion: bench.CurrentPlanSchema,
			FileSize:      size,
			ETag:          etag,
			ReadSize:      *readSize,
			Seed:          *seed,
			Pattern:       bench.Pattern(*pattern),
			Ranges:        plan,
		})
		closeErr := file.Close()
		if saveErr != nil {
			fmt.Fprintln(os.Stderr, "plan-out:", saveErr)
			os.Exit(1)
		}
		if closeErr != nil {
			fmt.Fprintln(os.Stderr, "plan-out close:", closeErr)
			os.Exit(1)
		}
	}

	httpStats, err := bench.MeasureHTTPBatched(
		ctx,
		client,
		*serverURL,
		*fileID,
		etag,
		plan,
		*batch,
		options,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "HTTP benchmark:", err)
		os.Exit(1)
	}
	results := []bench.Stats{httpStats}

	if *localPath != "" {
		localStats, err := bench.MeasureFile(*localPath, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "baseline benchmark:", err)
			os.Exit(1)
		}
		results = append(results, localStats)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	for _, result := range results {
		fmt.Printf(
			"%s reads=%d requests=%d bytes=%d elapsed=%.2fms MiB/s=%.2f p50=%.3fms p95=%.3fms max=%.3fms\n",
			result.Source,
			result.Reads,
			result.Requests,
			result.Bytes,
			result.ElapsedMS,
			result.MiBPerSecond,
			result.P50MS,
			result.P95MS,
			result.MaxMS,
		)
	}
}
