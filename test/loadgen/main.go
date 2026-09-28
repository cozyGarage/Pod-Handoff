/*
Copyright 2026 The PodHandoff Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type result struct {
	started time.Time
	status  int
	latency time.Duration
	backend string
	err     string
}

func main() {
	url := flag.String("url", "http://127.0.0.1:30080/work", "request URL")
	rate := flag.Int("rate", 50, "offered requests per second")
	duration := flag.Duration("duration", 2*time.Minute, "run duration")
	concurrency := flag.Int("concurrency", 128, "maximum in-flight requests")
	out := flag.String("out", "load.csv", "CSV output path")
	flag.Parse()
	if *rate < 1 || *duration <= 0 || *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "rate, duration, and concurrency must be positive")
		os.Exit(2)
	}

	file, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer file.Close()
	w := csv.NewWriter(file)
	if err := w.Write([]string{"timestamp_utc", "http_status", "latency_ms", "backend_pod", "error"}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	interval := time.Second / time.Duration(*rate)
	jobs := make(chan time.Time, *concurrency)
	results := make(chan result, *concurrency)
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	var workers sync.WaitGroup
	for range *concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for started := range jobs {
				request, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, *url, nil)
				if reqErr != nil {
					results <- result{started: started, err: reqErr.Error()}
					continue
				}
				begin := time.Now()
				response, reqErr := client.Do(request)
				entry := result{started: started, latency: time.Since(begin)}
				if reqErr != nil {
					entry.err = reqErr.Error()
				} else {
					entry.status = response.StatusCode
					entry.backend = response.Header.Get("X-Pod-Name")
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				results <- entry
			}
		}()
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for entry := range results {
			if err := w.Write([]string{
				entry.started.UTC().Format(time.RFC3339Nano),
				strconv.Itoa(entry.status),
				fmt.Sprintf("%.3f", float64(entry.latency)/float64(time.Millisecond)),
				entry.backend,
				entry.err,
			}); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
		w.Flush()
	}()

	deadline := time.Now().Add(*duration)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for now := range ticker.C {
		if now.After(deadline) {
			break
		}
		select {
		case jobs <- now:
		default:
			results <- result{started: now, err: "client concurrency limit"}
		}
	}
	close(jobs)
	workers.Wait()
	close(results)
	<-writerDone
}
