package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	stripe "github.com/stripe/stripe-go"
	paymentintent "github.com/stripe/stripe-go/paymentintent"
	"github.com/yabamuro/gocelery"
	"go.uber.org/zap"
)

const (
	productionMode       = false
	taskName             = "worker.execute"
	notifyTaskName       = "worker.notification"
	queue                = "payment"
	notification         = "notification"
	stgPubKey            = "pk_test_j5fvxJmoN3TlTaNTgcATv0W000HRwOI317"
	prodPubKey           = "*******"
	secStgKey            = "sk_test_v4QrE3LoY9Cu2ki3IwQylABI00Hbes7WQT"
	secProdKey           = "*******"
	updateRestockQuery   = "UPDATE PRODUCT_INFO SET STOCK='%v' WHERE PRODUCT_ID='%v'"
	getStockQuery        = "SELECT STOCK FROM PRODUCT_INFO WHERE PRODUCT_ID='%v'"
	layout               = "2006-01-02"
	transactionFieldName = "transaction"
)

var (
	celeryReqs = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rcv_req_celery_payexecution",
			Help: "How many Celery requests processed.",
		},
		[]string{"code", "method"},
	)
	celerySends = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "snd_resp_celery_payexecution",
			Help: "How many Celery sends processed.",
		},
		[]string{"code", "method"},
	)

	celeryClient      *gocelery.CeleryClient
	notifyClient      *gocelery.CeleryClient
	redisClient       *redis.Client
	ctx               context.Context
	redisServerName   string
	gomaxprocs        int
	nworker           int
	jobQueue          chan job
	jobQueueBufferize int
	logger            *zap.Logger
	debug             bool
)

type requestparam struct {
	transactionID string
	productID     string
	customerid    string
	dealStock     int32
	totalAmount   int32
	imageURL      string
	category      int32
	productName   string
	price         int32
	userID        string
	cardid        string
	address       string
	retryCnt      int
	restockFlag   bool
	status        string
}

type job struct {
	Request  requestparam
	Receiver chan []byte
}

func init() {
	testing.Init()
	prometheus.MustRegister(celeryReqs)

	// Setting Flag for Logging
	flag.BoolVar(&debug, "debug", false, "debug mode flag")
	flag.Parse()
	if debug {
		logger, _ = zap.NewDevelopment()
	} else {
		logger, _ = zap.NewProduction()
	}

	stripe.Key = secStgKey
}

func main() {
	// exec node-export service
	go exportMetrics()
	
	// Celery INIT
	redisServerName = os.Getenv("REDIS_SERVER")
	redisServerNameForCelery := "redis://" + redisServerName
	log.Println("[DEBUG] redisServerNameForCelery [", redisServerNameForCelery, "]")
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

	notifyClient, err = gocelery.NewCeleryClient(
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
	celeryClient = cli

	// go-redis init
	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisServerName,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

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

func execute(rp requestparam) int {

	countReqs()
	sendReqLog(rp)

	// initialize redis controller struct
	HashSet(redisClient, rp.transactionID, transactionFieldName, rp.status)

	// Connect DB(MySQL)
	db, err := connectDB()
	if err != nil {
		logger.Error(" DB Connection ERROR!! in worker.go:", zap.Error(err))
		return 400
	}
	defer db.Close()

	nowStocks, err := getStocks(rp.productID, db)

	if rp.restockFlag {
		logger.Debug("first restock route.")

		insertStock := nowStocks + int(rp.dealStock)
		updateStocks(rp.productID, insertStock, db)
		return 0
	}

	exclusion := SetNX(redisClient, rp.productID, "intrade")
	if exclusion {
		// debug
		logger.Debug("exclusion route.")

		if rp.retryCnt > 10 {
			_, err = notifyClient.Delay(notifyTaskName, rp.address, "The number of retries has been exceeded.　Please try again in a few minutes.")
			if err != nil {
				logger.Error("FAILER notification failed:", zap.Error(err))
			}
			DELETE(redisClient, rp.productID)
		}

		time.Sleep(10 * time.Second)
		rp.retryCnt = rp.retryCnt + 1
		rp.restockFlag = false
		rp.status = "start"
		_, err = celeryClient.Delay(taskName, rp)
		if err != nil {
			logger.Error("Enqueue Error:", zap.Error(err),
				zap.String("ProductId:", rp.productID),
				zap.String("TransactionID:", rp.transactionID),
				zap.Int("RetryCount:", rp.retryCnt))
			return 400
		}
	} else {
		st := HashGet(redisClient, rp.transactionID, transactionFieldName)

		switch st {
		case "start":
			st = startTransaction(rp.transactionID, rp.userID, rp.customerid, rp.cardid, rp.address, int(rp.totalAmount), rp.retryCnt)
			if st == "" {
				return 400
			}
			fallthrough

		case "succeeded":
			st, nowStocks = succeededTransaction(db, rp.transactionID, rp.productID, rp.imageURL, rp.productName, int(rp.category), int(rp.dealStock), int(rp.price), rp.restockFlag)
			if st == "" || nowStocks == -1 {
				return 400
			}
			fallthrough

		case "settlement_done":
			st = settleTransaction(rp.transactionID, rp.address, rp.productName)
			if st == "" {
				return 400
			}
			fallthrough

		case "notification_done":
			notificationTransaction(rp.transactionID, rp.productID)

		default:
			logger.Debug("Do Nothing. :", zap.Any("request:", rp))
			return 400

		}

		// mockten mock to restock function
		nowStocks, err = getStocks(rp.productID, db)
		if nowStocks < 5 || err != nil {
			logger.Debug("Change restock flg is TRUE!!")
			rp.dealStock = 5
			rp.restockFlag = true
			rp.status = "start"
			_, err = celeryClient.Delay(taskName, rp)
		}

		countSends()
		return 0
	}

	return 0
}

func startTransaction(transactionID, userID, customerid, cardid, address string, totalAmount, retryCnt int) string {
	logger.Debug("startTransaction.", zap.String("tID", transactionID))
	var status string

	if len(userID) == 0 {
		// TODO
		// BankAPIの使用(transfer_money)
	} else {
		// use strinp
		payid := requestPayment(customerid, totalAmount, address, retryCnt)
		if payid == "" || len(payid) <= 0 {
			logger.Error("Payid is nil:", zap.String("Customerid:", customerid))
			return ""
		}

		status = confirmPayment(cardid, payid)
		if status != "succeeded" {
			logger.Error("Authentication not successful:",
				zap.String("Payid:", payid),
				zap.String("Customerid:", customerid))
			return ""
		}

	}
	// Overwrite the result of payment completion to status
	HashSet(redisClient, transactionID, transactionFieldName, status)
	return status // succeeded
}

func succeededTransaction(db *sql.DB,
	transactionID, productID, imageURL, productName string,
	category, dealStock, price int,
	restockFlag bool) (string, int) {
	logger.Debug("[WORKER] succeeded route.")

	nowStocks, err := getStocks(productID, db)
	if err != nil {
		return "", -1
	}

	if !restockFlag {
		// Usually purchased
		// update redis
		t := timeToString(time.Now())
		zaddKey := fmt.Sprintf("%v_%v", t, category)
		zaddKey99 := fmt.Sprintf("%v_%v", t, "99")

		z := &redis.Z{}
		z.Score = float64(dealStock)
		z.Member = productID
		ZAdd(redisClient, zaddKey, z)
		ZAdd(redisClient, zaddKey99, z)

		// hsetValue := fmt.Sprintf("price:%v,imageURL:%v,name:%v", price, imageURL, productName)
		HashSet(redisClient, productID, "price", price)
		HashSet(redisClient, productID, "url", imageURL)
		HashSet(redisClient, productID, "name", productName)

		if nowStocks >= dealStock {
			logger.Debug("update stock route.", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", dealStock))
			insertStock := nowStocks - dealStock
			updateStocks(productID, insertStock, db)
		} else {
			logger.Debug("TooMuchDealStock...", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", dealStock))
			return "", -1
		}
	} else {
		// Execute restock
		// debug
		logger.Debug("restock route.")

		insertStock := nowStocks + dealStock
		updateStocks(productID, insertStock, db)
	}
	status := "settlement_done"
	HashSet(redisClient, transactionID, transactionFieldName, status)

	return status, nowStocks
}

func settleTransaction(transactionID, address, productName string) string {
	logger.Debug("settleTransaction.",
		zap.String("transactionID:", transactionID),
		zap.String("address:", address),
		zap.String("productName:", productName))

	_, err := notifyClient.Delay(notifyTaskName, address, fmt.Sprintf("The 'ProductName:[%v]' has been purchased.", productName))
	if err != nil {
		logger.Error("notification failed.")
		return ""
	}

	status := "notification_done"
	HashSet(redisClient, transactionID, transactionFieldName, status)
	return status
}

func notificationTransaction(transactionID, productID string) {
	logger.Debug("notificationTransaction.",
		zap.String("transactionID:", transactionID),
		zap.String("productID:", productID))

	DELETE(redisClient, productID)
	DELETE(redisClient, transactionID)
}

/*
Create a unique PaymentIntent in the order session.
This is for later retracing the purchase history, etc.
@see : https://stripe.com/docs/api/payment_intents
*/
func requestPayment(customerid string, totalAmount int, address string, retryCnt int) (payid string) {
	payparams := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(int64(totalAmount) / 10),
		Customer: stripe.String(customerid),
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		// Receipt_email: stripe.String(address),
		PaymentMethodTypes: stripe.StringSlice([]string{
			"card",
		}),
	}
	obj, err := paymentintent.New(payparams)
	if err != nil {
		logger.Error("requestPayment failed.:", zap.Error(err))
	}
	// One paymentid is paid out in one order
	v := reflect.ValueOf(*obj)
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Name == "ID" {
			payid = fmt.Sprintf("%v", v.Field(i).Interface())
		}
		//if v.Type().Field(i).Name == "Status" {
		//	status := fmt.Sprintf("%v", v.Field(i).Interface())
		//	if status != "requires_payment_method" && retryCnt > 0 {
		//		_, err = celery_client.Delay(TaskName, productID, customerid, dealStock, totalAmount, userID, cardid, address, retryCnt+1, false, "start")
		//	}
		//}
	}
	return payid
}

/*
Get the result of the payment.
@see : https://stripe.com/docs/payments/intents#intent-statuses
*/
func confirmPayment(cardid string, payid string) (status string) {
	params := &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(cardid),
	}
	pi, _ := paymentintent.Confirm(
		payid,
		params,
	)
	v := reflect.ValueOf(*pi)
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Name == "Status" {
			status = fmt.Sprintf("%v", v.Field(i).Interface())
		}
	}
	return status
}

func sendReqLog(rq requestparam) {
	log.Println(fmt.Printf("[WORKER] payexection request param %v", rq))
}

func timeToString(t time.Time) string {
	str := t.Format(layout)
	str = strings.Replace(str, "-", "", -1)
	return str
}

// HashSet :redis hash-set
// see : http://redis.shibu.jp/commandreference/hashes.html
func HashSet(redisClient *redis.Client, key, field string, value interface{}) {
	logger.Debug("HashSet.",
		zap.String("key:", key),
		zap.String("field:", field),
		zap.Any("value:", value))
	err := redisClient.HSet(ctx, key, field, value).Err()
	if err != nil {
		logger.Error("redis.Client.HSet Error:", zap.Error(err))
	}
}

// HashMSet :redis hash malti set
func HashMSet(redisClient *redis.Client, key, value string) {
	// Set
	logger.Debug("HashMSet.", zap.String("key:", key), zap.String("value:", value))
	err := redisClient.HMSet(ctx, key, value).Err()
	if err != nil {
		logger.Error("redis.Client.HMSet Error:", zap.Error(err))
	}
}

// HashGet :redis hash get
func HashGet(redisClient *redis.Client, key, field string) string {
	// Get
	// HGet(key, field string) *StringCmd
	logger.Debug("HashGet.", zap.String("key:", key), zap.String("field:", field))
	hgetVal, err := redisClient.HGet(ctx, key, field).Result()
	if err != nil {
		logger.Error("redis.Client.HGet Error:", zap.Error(err))
	}

	return hgetVal
}

// HashDelete : redis hash delete
func HashDelete(redisClient *redis.Client, key, field string) {
	logger.Debug("HashDelete.", zap.String("key:", key), zap.String("field:", field))
	err := redisClient.HDel(ctx, key, field).Err()
	if err != nil {
		logger.Error("redis.Client.HDel Error:", zap.Error(err))
	}
}

// Get : redis get
func Get(redisClient *redis.Client, key string) string {
	// Get
	logger.Debug("Get.", zap.String("key:", key))
	val, err := redisClient.Get(ctx, key).Result()
	if err != nil {
		logger.Error("redis.Client.Get Error:", zap.Error(err))
	}

	return val
}

// DELETE : redis delete
func DELETE(redisClient *redis.Client, key string) {
	logger.Debug("DELETE.", zap.String("key:", key))
	err := redisClient.Del(ctx, key).Err()
	if err != nil {
		logger.Error("redis.Client.Del Error:", zap.Error(err))
	}
}

// ZAdd : redis zadd
func ZAdd(redisClient *redis.Client, key string, z *redis.Z) {
	logger.Debug("ZAdd.", zap.String("key:", key), zap.Any("z:", z))
	err := redisClient.ZAdd(ctx, key, z).Err()
	if err != nil {
		logger.Error("redis.Client.ZAdd Error:", zap.Error(err))
	}
}

// SetNX : redis setnx
func SetNX(redisClient *redis.Client, key, value string) bool {
	logger.Debug("ZAdd.", zap.String("key:", key), zap.String("value:", value))
	res, err := redisClient.SetNX(ctx, key, value, 0).Result()
	if err != nil {
		logger.Error("redis.Client.SetNX Error:", zap.Error(err))
	}

	return res
}

// connect DB(mysql)
func connectDB() (*sql.DB, error) {
	user := os.Getenv("SECRET_USER")
	pass := os.Getenv("SECRET_PASS")
	sdb := os.Getenv("SECRET_DB")
	table := os.Getenv("SECRET_TABLE")

	mySQLHost := fmt.Sprintf("%s:%s@tcp(%s)/%s", user, pass, sdb, table)
	logger.Debug("connectDB.", zap.String("mysqlhost:", mySQLHost))
	db, err := sql.Open("mysql", mySQLHost)
	if err != nil {
		logger.Error("sql.Open():", zap.Error(err))
		return nil, err
	}

	//if err = db.Ping(); err != nil {
	//	log.Printf("[WORKER] db.Ping(): %s\n", err)
	//	return nil, err
	//}

	return db, nil
}

func getStocks(productID string, db *sql.DB) (int, error) {
	stockQuery := fmt.Sprintf(getStockQuery, productID)
	logger.Debug("getStocks.", zap.String("get stock query:", stockQuery))
	stocksRows, err := db.Query(stockQuery)
	if err != nil {
		logger.Error("SELECT stock Query Error: ",
			zap.Error(err),
			zap.String("Stock Query is:", stockQuery))
		return 0, err
	}
	defer stocksRows.Close()

	var stock int
	for stocksRows.Next() {
		if err := stocksRows.Scan(&stock); err != nil {
			logger.Error("Stocks Scan Error: ", zap.Error(err))
			return 0, err
		}
	}
	return stock, nil
}

func updateStocks(productID string, updateStocks int, db *sql.DB) {
	updateStockQuery := fmt.Sprintf(updateRestockQuery, updateStocks, productID)
	logger.Debug("updateStocks.", zap.String("update stock query :", updateStockQuery))
	_, err := db.Exec(updateStockQuery)
	if err != nil {
		logger.Error("Stocks Scan Error: ",
			zap.Error(err),
			zap.String(" |Query is:", updateStockQuery))
	}
}

// for goroutin
func exportMetrics() {
	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":9100", nil)
}

// count
func countReqs() {
	celeryReqs.WithLabelValues("pay.execution", "accept").Add(1)
}

func countSends() {
	celerySends.WithLabelValues("pay.execution", "send").Add(1)
}
