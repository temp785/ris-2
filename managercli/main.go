package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
)

const host = "http://localhost:8000"

type crackResponse struct {
	RequestID string `json:"requestId"`
}

type statusResponse struct {
	Status string   `json:"status"`
	Data   []string `json:"data"`
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "crack":
		if len(os.Args) != 4 {
			printUsage()
			os.Exit(1)
		}

		hash := os.Args[2]
		maxLen := os.Args[3]

		err := runCrack(hash, maxLen)
		if err != nil {
			fmt.Println("Ошибка:", err)
			os.Exit(1)
		}
	case "status":
		if len(os.Args) != 3 {
			printUsage()
			os.Exit(1)
			os.Exit(1)
		}

		requestID := os.Args[2]

		err := runStatus(requestID)
		if err != nil {
			fmt.Println("Ошибка:", err)
			os.Exit(1)
		}
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Использование:")
	fmt.Println("  go run . crack <hash> <maxLen>")
	fmt.Println("  go run . status <requestId>")
}

func runCrack(hash string, maxLen string) error {
	fmt.Println("Отправка задачи на подбор хеша...")

	body := map[string]any{
		"hash":      hash,
		"maxLength": mustAtoi(maxLen),
	}

	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return err
	}

	resp, err := http.Post(
		host+"/api/hash/crack",
		"application/json",
		bytes.NewBuffer(bodyJSON),
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("сервер вернул статус %d", resp.StatusCode)
	}

	var result crackResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	fmt.Println("Задача принята сервером ✅")
	fmt.Println("Request ID:", result.RequestID)
	fmt.Println("Проверить статус:")
	fmt.Printf("  go run . status %s\n", result.RequestID)

	return nil
}

func runStatus(requestID string) error {
	u, err := url.Parse(host + "/api/hash/status")
	if err != nil {
		return err
	}

	q := u.Query()
	q.Set("requestId", requestID)
	u.RawQuery = q.Encode()

	resp, err := http.Get(u.String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("задача не найдена")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("сервер вернул статус %d", resp.StatusCode)
	}

	var result statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	switch result.Status {
	case "IN_PROGRESS":
		fmt.Println("Статус: В процессе ⏳")
	case "READY":
		fmt.Println("Статус: Готово ✅")
		fmt.Println("Результаты:")
		for _, v := range result.Data {
			fmt.Println(" -", v)
		}
	case "ERROR":
		fmt.Println("Статус: Ошибка ❌")
	default:
		fmt.Println("Неизвестный статус:", result.Status)
	}

	return nil
}

func mustAtoi(s string) int {
	var n int
	_, err := fmt.Sscan(s, &n)
	if err != nil {
		fmt.Println("maxLen должен быть числом")
		os.Exit(1)
	}
	return n
}
