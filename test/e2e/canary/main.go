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
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "podhandoff e2e canary")
	})
	handler.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ready")
	})
	handler.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Pod-Name", hostname)
		iterations := 500_000
		if raw := r.URL.Query().Get("iterations"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 10_000_000 {
				http.Error(w, "iterations must be between 1 and 10000000", http.StatusBadRequest)
				return
			}
			iterations = parsed
		}
		sum := sha256.Sum256([]byte("podhandoff load canary"))
		for i := 0; i < iterations; i++ {
			sum = sha256.Sum256(sum[:])
		}
		_, _ = fmt.Fprintf(w, "%x\n", sum)
	})
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
