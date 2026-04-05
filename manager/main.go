package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/isayme/go-amqp-reconnect/rabbitmq"
	"github.com/streadway/amqp"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type Status string

const (
	StatusInProgress Status = "IN_PROGRESS"
	StatusReady      Status = "READY"
	StatusError      Status = "ERROR"
)

const (
	chunkSize = 100_000
	alphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
)

var (
	mongoClient *mongo.Client
	tasksColl   *mongo.Collection
	rabbitCh    *rabbitmq.Channel
)

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func totalCombinations(maxLen int) uint64 {
	base := uint64(len(alphabet))
	var total uint64
	pow := uint64(1)
	for i := 1; i <= maxLen; i++ {
		pow *= base
		total += pow
	}
	return total
}

func main() {
	ctx := context.Background()

	mongoURI := getEnv("MONGO_URI", "mongodb://mongo-0.mongo.hashcrack.svc.cluster.local:27017,mongo-1.mongo.hashcrack.svc.cluster.local:27017,mongo-2.mongo.hashcrack.svc.cluster.local:27017/?replicaSet=rs0")
	clientOpts := options.Client().ApplyURI(mongoURI).SetWriteConcern(writeconcern.Majority())
	var err error
	mongoClient, err = mongo.Connect(ctx, clientOpts)
	if err != nil {
		fmt.Printf("MongoDB connect error: %v\n", err)
		panic(err)
	}
	tasksColl = mongoClient.Database("hashcrack").Collection("tasks")

	rabbitURI := getEnv("RABBIT_URI", "amqp://guest:guest@rabbitmq:5672/")
	conn, err := rabbitmq.Dial(rabbitURI)
	if err != nil {
		fmt.Printf("RabbitMQ dial error: %v\n", err)
		panic(err)
	}
	rabbitmq.Debug = true
	rabbitCh, err = conn.Channel()
	if err != nil {
		fmt.Printf("RabbitMQ channel error: %v\n", err)
		panic(err)
	}
	if err := declareQueues(); err != nil {
		fmt.Printf("RabbitMQ declare queues error: %v\n", err)
		panic(err)
	}

	go resultConsumer(ctx)
	go retryPendingTasks(ctx)

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	r.GET("/hc", hc)
	r.POST("/api/hash/crack", crack)
	r.GET("/api/hash/status", statusHandler)

	fmt.Println("Manager started on :8000")
	_ = r.Run(":8000")
}

func declareQueues() error {
	err := rabbitCh.ExchangeDeclare("task_exchange", "direct", true, false, false, false, nil)
	if err != nil {
		return err
	}
	_, err = rabbitCh.QueueDeclare("tasks", true, false, false, false, nil)
	if err != nil {
		return err
	}
	err = rabbitCh.QueueBind("tasks", "task", "task_exchange", false, nil)
	if err != nil {
		return err
	}

	err = rabbitCh.ExchangeDeclare("result_exchange", "direct", true, false, false, false, nil)
	if err != nil {
		return err
	}
	_, err = rabbitCh.QueueDeclare("results", true, false, false, false, nil)
	if err != nil {
		return err
	}
	return rabbitCh.QueueBind("results", "result", "result_exchange", false, nil)
}

func resultConsumer(ctx context.Context) {
	msgs, err := rabbitCh.Consume("results", "", false, false, false, false, nil)
	if err != nil {
		fmt.Printf("[result] consume: %v", err)
		return
	}
	for msg := range msgs {
		var res struct {
			RequestID  string   `json:"requestId"`
			ChunkIndex int      `json:"chunkIndex"`
			Answers    []string `json:"answers"`
		}
		if err := json.Unmarshal(msg.Body, &res); err != nil {
			fmt.Printf("[result] Unmarshal error: %v\n", err)
			msg.Nack(false, true)
			continue
		}

		fmt.Printf("[result] Received from worker: request=%s chunk=%d answers=%d\n",
			res.RequestID, res.ChunkIndex, len(res.Answers))

		update := bson.M{
			"$set": bson.M{
				fmt.Sprintf("chunks.%d.done", res.ChunkIndex):    true,
				fmt.Sprintf("chunks.%d.results", res.ChunkIndex): res.Answers,
			},
		}

		if len(res.Answers) > 0 {
			update["$addToSet"] = bson.M{"data": bson.M{"$each": res.Answers}}
		}

		_, err := tasksColl.UpdateOne(ctx,
			bson.M{"requestId": res.RequestID},
			update)

		if err != nil {
			fmt.Printf("[result] Mongo update error: %v\n", err)
			msg.Nack(false, true)
			continue
		}

		var task bson.M
		if err := tasksColl.FindOne(ctx, bson.M{"requestId": res.RequestID}).Decode(&task); err == nil {
			allDone := true
			chunks := task["chunks"].(bson.A)
			for _, ch := range chunks {
				if m, ok := ch.(bson.M); ok && !m["done"].(bool) {
					allDone = false
					break
				}
			}
			if allDone {
				_, err = tasksColl.UpdateOne(ctx,
					bson.M{"requestId": res.RequestID},
					bson.M{"$set": bson.M{"status": StatusReady}})
				if err != nil {
					fmt.Printf("[result] Mongo update done error: %v\n", err)
					msg.Nack(false, true)
					continue
				}
				fmt.Printf("[result] Task %s completed → READY\n", res.RequestID)
			}
		}

		msg.Ack(false)
	}
}

func retryPendingTasks(ctx context.Context) {
	for {
		time.Sleep(2 * time.Minute)
		cur, err := tasksColl.Find(ctx, bson.M{"status": StatusInProgress})
		if err != nil {
			fmt.Printf("[retry] find error: %v", err)
			continue
		}
		for cur.Next(ctx) {
			var t struct {
				RequestID string `bson:"requestId"`
				Hash      string `bson:"hash"`
				MaxLength int    `bson:"maxLength"`
				Chunks    []struct {
					Index int  `bson:"index"`
					Done  bool `bson:"done"`
				} `bson:"chunks"`
			}
			err = cur.Decode(&t)
			if err != nil {
				fmt.Printf("[retry] decode error: %v", err)
				continue
			}

			for _, ch := range t.Chunks {
				if ch.Done {
					continue
				}
				fmt.Printf("[retry] Republishing chunk %d for task %s\n", ch.Index, t.RequestID)

				body := map[string]any{
					"requestId":  t.RequestID,
					"chunkIndex": ch.Index,
					"hash":       t.Hash,
					"maxLength":  t.MaxLength,
					"alphabet":   alphabet,
				}
				b, _ := json.Marshal(body)
				err = rabbitCh.Publish("task_exchange", "task", false, false, amqp.Publishing{
					DeliveryMode: amqp.Persistent,
					ContentType:  "application/json",
					Body:         b,
				})
				if err != nil {
					fmt.Printf("[retry] publish: %v", err)
					continue
				}
			}
		}
		err = cur.Close(ctx)
		if err != nil {
			fmt.Printf("[retry] close error: %v", err)
			continue
		}
	}
}

func hc(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

type crackReq struct {
	Hash      string `json:"hash" validate:"required"`
	MaxLength int    `json:"maxLength" validate:"gt=0"`
}

func crack(c *gin.Context) {
	var req crackReq
	if err := c.BindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validator.New().Struct(req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	requestID := uuid.New().String()
	total := totalCombinations(req.MaxLength)
	numChunks := (total + uint64(chunkSize) - 1) / uint64(chunkSize)

	chunks := make([]bson.M, 0, numChunks)
	for i := 0; i < int(numChunks); i++ {
		start := uint64(i) * uint64(chunkSize)
		size := uint64(chunkSize)
		if start+size > total {
			size = total - start
		}
		chunks = append(chunks, bson.M{
			"index":   i,
			"start":   start,
			"size":    size,
			"done":    false,
			"results": []string{},
		})
	}

	taskDoc := bson.M{
		"requestId": requestID,
		"status":    StatusInProgress,
		"hash":      req.Hash,
		"maxLength": req.MaxLength,
		"chunks":    chunks,
		"data":      []string{},
		"createdAt": time.Now(),
	}

	if _, err := tasksColl.InsertOne(c, taskDoc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	go func() {
	pub:
		for {
			for _, ch := range chunks {
				body := map[string]any{
					"requestId":  requestID,
					"chunkIndex": ch["index"],
					"start":      ch["start"],
					"size":       ch["size"],
					"hash":       req.Hash,
					"maxLength":  req.MaxLength,
					"alphabet":   alphabet,
				}
				b, _ := json.Marshal(body)
				err := rabbitCh.Publish("task_exchange", "task", false, false, amqp.Publishing{
					DeliveryMode: amqp.Persistent,
					ContentType:  "application/json",
					Body:         b,
				})
				if err != nil {
					time.Sleep(2 * time.Second)
					continue pub
				}
			}
			return
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"requestId": requestID})
}

type statusReq struct {
	RequestID string `form:"requestId" validate:"required"`
}

func statusHandler(c *gin.Context) {
	var req statusReq
	if err := c.BindQuery(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validator.New().Struct(req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var task bson.M
	if err := tasksColl.FindOne(c, bson.M{"requestId": req.RequestID}).Decode(&task); err != nil {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	createdAt := task["createdAt"].(primitive.DateTime).Time()
	maxLen := int(task["maxLength"].(int32))
	timeout := time.Duration(maxLen*maxLen) * 30 * time.Second

	if time.Since(createdAt) > timeout && task["status"] == StatusInProgress {
		_, err := tasksColl.UpdateOne(c,
			bson.M{"requestId": req.RequestID},
			bson.M{"$set": bson.M{"status": StatusError}})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		task["status"] = StatusError
		fmt.Printf("[timeout] Task %s marked as ERROR\n", req.RequestID)
	}

	c.JSON(http.StatusOK, gin.H{
		"status": task["status"],
		"data":   task["data"],
	})
}
