package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	stripe "github.com/stripe/stripe-go"
	"github.com/yabamuro/gocelery"
	"go.uber.org/zap"
)

var (
	pj              *paymentjob
	celJob          *celeryJob
	celeryClient    *gocelery.CeleryClient
	notifyClient    *gocelery.CeleryClient
	redisClient     *redis.Client
	ctx             context.Context
	redisServerName string
	logger          *zap.Logger
	debug           bool

	// Get Value from ConfigMap.
	taskName             = os.Getenv("taskName")
	notifyTaskName       = os.Getenv("notifyTaskName")
	queue                = os.Getenv("queue")
	notification         = os.Getenv("notification")
	publicStripeKey      = os.Getenv("pubKey")
	secretStripeKey      = os.Getenv("secKey")
	transactionFieldName = os.Getenv("transactionFieldName")
)

func init() {
	testing.Init()

	// Setting Flag for Logging
	flag.BoolVar(&debug, "debug", false, "debug mode flag")
	flag.Parse()
	if debug {
		logger, _ = zap.NewDevelopment()
	} else {
		logger, _ = zap.NewProduction()
	}

}

func main() {
	verify()
	stripe.Key = secretStripeKey
	// exec node-export service
	go exportMetrics()

	// Celery INIT
	redisServerName := fmt.Sprintf("%s:%s", os.Getenv("REDIS_SERVICE_HOST"), os.Getenv("REDIS_SERVICE_PORT"))
	redisServerNameForCelery := "redis://" + redisServerName
	logger.Sugar().Debugf("redisServerNameForCelery : %s", redisServerNameForCelery)
	concurrency := 3
	cli, err := gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker(redisServerNameForCelery, queue),
		gocelery.NewRedisCeleryBackend(redisServerNameForCelery),
		concurrency,
	)
	log.Println("[DEBUG] Celery Client [", cli, "]")
	if err != nil {
		log.Println("Execute Celery doesnt connect Redis. [", err, "]")
	}

	notifyClient, err := gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker(redisServerNameForCelery, notification),
		gocelery.NewRedisCeleryBackend(redisServerNameForCelery),
		1,
	)
	log.Println("[DEBUG] Notification Client [", notifyClient, "]")
	if err != nil {
		log.Println("Notification Celery doesnt connect Redis. [", err, "]")
	}

	cli.Register("worker.execute", execute)
	cli.StartWorker()
	defer cli.StopWorker()
	log.Println(fmt.Printf("[WORKER] worker start: concurrency=%v\n", concurrency))
	
	celJob = &celeryJob{celeryManager: newCelery(taskName, notifyTaskName, cli, notifyClient)}

	// go-redis init
	pj = &paymentjob{redisManager: newRedis(redisServerName, "", 0)}

	ctx = context.Background()
	ctxLocal, cancel := context.WithTimeout(ctx, 5*time.Hour)
	defer cancel()
	ctx = ctxLocal
	for i := 0; i < 10; i++ {
		pong, err := redisClient.Ping(ctx).Result()
		log.Println(i, pong, err)
		if err == nil {
			log.Println("connection!!")
			break
		}
	}
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	select {
	case sig := <-c:
		log.Println(fmt.Println("worker stopped by signal:", sig))
		return
	}
}

func verify() {
	if taskName == "" {
		panic("val: taskName is empty")
	} else if notifyTaskName == "" {
		panic("val: notifyTaskName is empty")
	} else if queue == "" {
		panic("val: queue is empty")
	} else if notification == "" {
		panic("val: notification is empty")
	} else if publicStripeKey == "" {
		panic("val: publicStripeKey is empty")
	} else if secretStripeKey == "" {
		panic("val: secretStripeKey is empty")
	} else if transactionFieldName == "" {
		panic("val: transactionFieldName is empty")
	}
	logger.Info("payexecution verify passed!")

}

// for goroutin
func exportMetrics() {
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":9100", nil)
}
