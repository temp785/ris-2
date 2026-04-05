package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const host = "http://localhost:8000"

var client = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        10000,
		MaxIdleConnsPerHost: 10000,
		MaxConnsPerHost:     10000,
	},
}

func main() {
	hash := flag.String("hash", "", "md5 hash")
	maxLen := flag.Int("maxLen", 4, "max length")

	flag.Parse()

	if *hash == "" {
		panic("hash required")
	}

	fmt.Println("🚀 Concurrent round load test")

	workers := 1

	for {
		fmt.Printf("\n=== ROUND %d concurrent requests ===\n", workers)

		snd, status := runRound(workers, *hash, *maxLen)

		if snd {
			fmt.Println("🧱 SERVER STOPPED RESPONDING")
			fmt.Println("💥 LIMIT FOUND at", workers, "concurrent requests")
			return
		}

		if status {
			fmt.Println("💥 SERVER RETURNED 500")
			fmt.Println("💥 LIMIT FOUND at", workers, "concurrent requests")
			return
		}

		fmt.Println("OK")
		workers *= 2
	}
}

func runRound(n int, hash string, maxLen int) (sn bool, status bool) {
	var wg sync.WaitGroup

	var snd atomic.Bool
	var got500 atomic.Bool

	done := make(chan struct{})

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()

			sn, status := send(hash, maxLen)

			if sn {
				snd.Store(true)
			}

			if status {
				got500.Store(true)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	// ждём завершения всех запросов
	select {
	case <-done:
		return snd.Load(), got500.Load()
	case <-time.After(10 * time.Second):
		// если через минуту не завершились — сервер завис
		return true, false
	}
}

func send(hash string, maxLen int) (send bool, status bool) {
	body := map[string]any{
		"hash":      hash,
		"maxLength": maxLen,
	}

	payload, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", host+"/api/hash/crack", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return true, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusInternalServerError {
		return false, true
	}

	return false, false
}

