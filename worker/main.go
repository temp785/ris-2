package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"worker/comb"

	"github.com/gin-gonic/gin"
	"github.com/isayme/go-amqp-reconnect/rabbitmq"
	"github.com/streadway/amqp"
)

type taskRequest struct {
	RequestID  string `json:"requestId"`
	ChunkIndex int    `json:"chunkIndex"`
	Start      uint64 `json:"start"`
	Size       uint64 `json:"size"`
	Hash       string `json:"hash"`
	MaxLength  int    `json:"maxLength"`
	Alphabet   string `json:"alphabet"`
}

type resultMsg struct {
	RequestID  string   `json:"requestId"`
	ChunkIndex int      `json:"chunkIndex"`
	Answers    []string `json:"answers"`
}

var rabbitCh *rabbitmq.Channel

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	rabbitURI := getEnv("RABBIT_URI", "amqp://guest:guest@rabbitmq:5672/")
	conn, err := rabbitmq.Dial(rabbitURI)
	if err != nil {
		fmt.Printf("RabbitMQ dial error: %v\n", err)
		panic(err)
	}
	rabbitCh, err = conn.Channel()
	if err != nil {
		fmt.Printf("RabbitMQ channel error: %v\n", err)
		panic(err)
	}

	if err := declareQueues(); err != nil {
		fmt.Printf("RabbitMQ declare queues error: %v\n", err)
		panic(err)
	}

	msgs, err := rabbitCh.Consume("tasks", "", false, false, false, false, nil)
	if err != nil {
		fmt.Printf("Consume tasks error: %v\n", err)
		panic(err)
	}

	go func() {
		for msg := range msgs {
			var task taskRequest
			if err := json.Unmarshal(msg.Body, &task); err != nil {
				fmt.Printf("Unmarshal task error: %v\n", err)
				msg.Nack(false, true)
				continue
			}

			answers := crack(task)

			res := resultMsg{
				RequestID:  task.RequestID,
				ChunkIndex: task.ChunkIndex,
				Answers:    answers,
			}
			b, _ := json.Marshal(res)
			if err := rabbitCh.Publish("result_exchange", "result", false, false, amqp.Publishing{
				DeliveryMode: amqp.Persistent,
				ContentType:  "application/json",
				Body:         b,
			}); err != nil {
				fmt.Printf("Publish result error: %v\n", err)
				msg.Nack(false, true)
				continue
			}

			msg.Ack(false)
		}
	}()

	r := gin.New()
	r.GET("/hc", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	fmt.Println("Worker started on :9000")
	_ = r.Run(":9000")
}

func declareQueues() error {
	if err := rabbitCh.ExchangeDeclare("task_exchange", "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := rabbitCh.QueueDeclare("tasks", true, false, false, false, nil); err != nil {
		return err
	}
	if err := rabbitCh.QueueBind("tasks", "task", "task_exchange", false, nil); err != nil {
		return err
	}

	if err := rabbitCh.ExchangeDeclare("result_exchange", "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := rabbitCh.QueueDeclare("results", true, false, false, false, nil); err != nil {
		return err
	}
	return rabbitCh.QueueBind("results", "result", "result_exchange", false, nil)
}

func crack(req taskRequest) []string {
	var answers []string
	comb.CombRange(req.Start, req.Size, req.Alphabet, req.MaxLength, func(word []byte) bool {
		if len(word) == 0 {
			return false
		}
		h := md5.Sum(word)
		if hex.EncodeToString(h[:]) == req.Hash {
			answers = append(answers, string(word))
		}
		return false
	})
	return answers
}
