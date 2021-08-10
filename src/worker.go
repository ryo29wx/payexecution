package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	stripe "github.com/stripe/stripe-go"
	paymentintent "github.com/stripe/stripe-go/paymentintent"
	"github.com/yabamuro/gocelery"
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

	celeryClient    *gocelery.CeleryClient
	notifyClient    *gocelery.CeleryClient
	redisClient     *redis.Client
	ctx             context.Context
	redisServerName string
)

func init() {
	prometheus.MustRegister(celeryReqs)
	redisServerName = os.Getenv("REDIS_SERVER")
}

func main() {
	// exec node-export service
	go exportMetrics()

	concurrency := 3
	stripe.Key = secStgKey
	cli, _ := gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker(redisServerName, queue),
		gocelery.NewRedisCeleryBackend(redisServerName),
		concurrency,
	)

	notifyClient, _ = gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker(redisServerName, notification),
		gocelery.NewRedisCeleryBackend(redisServerName),
		1,
	)

	redisClient = redis.NewClient(&redis.Options{
		Addr:     redisServerName,
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	cli.Register("worker.execute", execute)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	cli.StartWorker()
	defer cli.StopWorker()
	fmt.Printf("[WORKER] worker start: concurrency=%v\n", concurrency)

	celeryClient = cli

	ctx = context.Background()
	ctxLocal, cancel := context.WithTimeout(ctx, 5*time.Hour)
	defer cancel()
	ctx = ctxLocal

	pong, err := redisClient.Ping(ctx).Result()
	log.Println(pong, err)

	select {
	case sig := <-c:
		fmt.Println("worker stopped by signal:", sig)
		return
	}
}

func execute(transactionID string,
	productID string,
	customerid string,
	dealStock int,
	totalAmount int,
	imageURL string,
	category int,
	productName string,
	price int,
	userID string,
	cardid string,
	address string,
	retryCnt int,
	restockFlag bool,
	status string) int {

	countReqs()
	sendReqLog(transactionID, productID, customerid, dealStock, totalAmount, userID, cardid, address, retryCnt, restockFlag, status)

	// initialize redis controller struct
	HashSet(redisClient, transactionID, transactionFieldName, status)

	// Connect DB(MySQL)
	db, err := connectDB()
	if err != nil {
		log.Printf("[WORKER] DB Connection ERROR!! in worker.go")
		return 400
	}
	defer db.Close()

	nowStocks, err := getStocks(productID, db)

	if restockFlag {
		log.Println("[WORKER] first restock route.")

		insertStock := nowStocks + dealStock
		updateStocks(productID, insertStock, db)
		return 0
	}

	exclusion := SetNX(redisClient, productID, "intrade")
	if exclusion {
		// debug
		log.Println("[WORKER] exclusion route.")

		if retryCnt > 10 {
			_, err = notifyClient.Delay(notifyTaskName, address, "The number of retries has been exceeded.　Please try again in a few minutes.")
			if err != nil {
				log.Printf("[WORKER] FAILER notification failed.")
			}
			DELETE(redisClient, productID)
		}

		time.Sleep(10 * time.Second)
		_, err = celeryClient.Delay(taskName, transactionID, productID, customerid, dealStock, totalAmount, imageURL, category, productName, price, userID, cardid, address, retryCnt+1, false, "start")
		if err != nil {
			log.Printf("[WORKER] Enqueue Error:%v(ProductId:%v,TransactionId:%v,RetryCount:%v)", err, productID, transactionID, retryCnt)
			return 400
		}
	} else {
		st := HashGet(redisClient, transactionID, transactionFieldName)

		switch st {
		case "start":
			st = startTransaction(transactionID, userID, customerid, cardid, address, totalAmount, retryCnt)
			if st == "" {
				return 400
			}
			fallthrough

		case "succeeded":
			st, nowStocks = succeededTransaction(db, transactionID, productID, imageURL, productName, category, dealStock, price, restockFlag)
			if st == "" || nowStocks == -1 {
				return 400
			}
			fallthrough

		case "settlement_done":
			st = settleTransaction(transactionID, address, productName)
			if st == "" {
				return 400
			}
			fallthrough

		case "notification_done":
			notificationTransaction(transactionID, productID)

		default:
			log.Println("[WORKER] Do Nothing...")
			return 400

		}

		// mockten mock to restock function
		nowStocks, err = getStocks(productID, db)
		if nowStocks < 5 || err != nil {
			log.Println("[WORKER] Change restock flg is TRUE!!")
			_, err = celeryClient.Delay(taskName, transactionID, productID, customerid, 5, totalAmount, imageURL, category, productName, price, userID, cardid, address, retryCnt, true, "start")
		}

		countSends()
		return 0
	}

	return 0
}

func startTransaction(transactionID, userID, customerid, cardid, address string, totalAmount, retryCnt int) string {
	log.Println("[WORKER] start route.")
	var status string

	if len(userID) == 0 {
		// TODO
		// BankAPIの使用(transfer_money)
	} else {
		// use strinp
		payid := requestPayment(customerid, totalAmount, address, retryCnt)
		if payid == "" || len(payid) <= 0 {
			log.Printf("[WORKER] payid is nil. customerid is [%v].", customerid)
			return ""
		}

		status = confirmPayment(cardid, payid)
		if status != "succeeded" {
			log.Printf("[WORKER] Authentication not successful.  payid:[%v] | customerid:[%v].", payid, customerid)
			return ""
		}

	}
	// Overwrite the result of payment completion to status
	log.Println("[WORKER] start route done.")
	HashSet(redisClient, transactionID, transactionFieldName, status)
	return status // succeeded
}

func succeededTransaction(db *sql.DB,
	transactionID, productID, imageURL, productName string,
	category, dealStock, price int,
	restockFlag bool) (string, int) {
	log.Println("[WORKER] succeeded route.")

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
			log.Println("[WORKER] update stock route.")
			insertStock := nowStocks - dealStock
			updateStocks(productID, insertStock, db)
		} else {
			log.Println("[WORKER] The amount customer want to purchase is higher than the number of items in stock.")
			return "", -1
		}
	} else {
		// Execute restock
		// debug
		log.Println("[WORKER] restock route.")

		insertStock := nowStocks + dealStock
		updateStocks(productID, insertStock, db)
	}
	status := "settlement_done"
	HashSet(redisClient, transactionID, transactionFieldName, status)

	return status, nowStocks
}

func settleTransaction(transactionID, address, productName string) string {
	log.Println("[WORKER] settlement done route.")

	_, err := notifyClient.Delay(notifyTaskName, address, fmt.Sprintf("The 'ProductName:[%v]' has been purchased.", productName))
	if err != nil {
		log.Printf("[WORKER] SUCCESS notification failed.")
		return ""
	}

	status := "notification_done"
	HashSet(redisClient, transactionID, transactionFieldName, status)
	return status
}

func notificationTransaction(transactionID, productID string) {
	log.Println("[WORKER] notification done route.")

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
		fmt.Printf("err: %s\n", err)
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

func sendReqLog(transactionID string,
	productID string,
	customerid string,
	dealStock int,
	totalAmount int,
	userID string,
	cardid string,
	address string,
	retryCnt int,
	restockFlag bool,
	status string) {

	log.Printf("[WORKER] transactionID:[%v] | productID:[%v] | customerid:[%v] | dealStock:[%v] | totalAmount:[%v] | userID:[%v] | cardid:[%v] | address:[%v] | retryCnt:[%v] | restockFlag:[%v] | status:[%v]", transactionID, productID, customerid, dealStock, totalAmount, userID, cardid, address, retryCnt, restockFlag, status)
}

func timeToString(t time.Time) string {
	str := t.Format(layout)
	str = strings.Replace(str, "-", "", -1)
	return str
}

// HashSet :redis hash-set
// see : http://redis.shibu.jp/commandreference/hashes.html
func HashSet(redisClient *redis.Client, key, field string, value interface{}) {
	log.Printf("[WORKER] redis.Client.HSet KEY: %v FIELD: %v VALUE: %v", key, field, value)
	err := redisClient.HSet(ctx, key, field, value).Err()
	if err != nil {
		fmt.Println("[WORKER] redis.Client.HSet Error:", err)
	}
}

// HashMSet :redis hash malti set
func HashMSet(redisClient *redis.Client, key, value string) {
	// Set
	log.Printf("[WORKER] redis.Client.HMSet KEY: %v VALUE: %v", key, value)
	err := redisClient.HMSet(ctx, key, value).Err()
	if err != nil {
		fmt.Println("[WORKER] redis.Client.HMSet Error:", err)
	}
}

// HashGet :redis hash get
func HashGet(redisClient *redis.Client, key, field string) string {
	// Get
	// HGet(key, field string) *StringCmd

	hgetVal, err := redisClient.HGet(ctx, key, field).Result()
	if err != nil {
		fmt.Println("[WORKER] redis.Client.HGet Error:", err)
	}
	fmt.Println(hgetVal)

	return hgetVal
}

// HashDelete : redis hash delete
func HashDelete(redisClient *redis.Client, key, field string) {
	err := redisClient.HDel(ctx, key, field).Err()
	if err != nil {
		fmt.Println("redis.Client.HDel Error:", err)
	}
}

// Get : redis get
func Get(redisClient *redis.Client, key string) string {
	// Get
	log.Printf("redis.Client.Get KEY: %v", key)
	val, err := redisClient.Get(ctx, key).Result()
	if err != nil {
		fmt.Println("redis.Client.Get Error:", err)
	}
	fmt.Println(val)

	return val
}

// DELETE : redis delete
func DELETE(redisClient *redis.Client, key string) {
	err := redisClient.Del(ctx, key).Err()
	if err != nil {
		fmt.Println("redis.Client.Del Error:", err)
	}
}

// ZAdd : redis zadd
func ZAdd(redisClient *redis.Client, key string, z *redis.Z) {
	// Get
	log.Printf("redis.Client.ZAdd KEY: %v", key)
	err := redisClient.ZAdd(ctx, key, z).Err()
	if err != nil {
		fmt.Println("redis.Client.ZAdd Error:", err)
	}
}

// SetNX : redis setnx
func SetNX(redisClient *redis.Client, key, value string) bool {
	log.Printf("redis.Client.SetNX KEY: %v VALUE: %v", key, value)
	res, err := redisClient.SetNX(ctx, key, value, 0).Result()
	if err != nil {
		fmt.Println("redis.Client.SetNX Error:", err)
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
	log.Printf("[WORKER] MySQL Host ... %v .", mySQLHost)
	db, err := sql.Open("mysql", mySQLHost)
	if err != nil {
		log.Printf("[WORKER] sql.Open(): %s\n", err)
		return nil, err
	}

	if err = db.Ping(); err != nil {
		log.Printf("[WORKER] db.Ping(): %s\n", err)
		return nil, err
	}

	return db, nil
}

func getStocks(productID string, db *sql.DB) (int, error) {
	stockQuery := fmt.Sprintf(getStockQuery, productID)
	stocksRows, err := db.Query(stockQuery)
	if err != nil {
		log.Printf("[WORKER] SELECT stock Query Error: %v | Stock Query is: %v ", err, stockQuery)
		return 0, err
	}
	defer stocksRows.Close()

	var stock int
	for stocksRows.Next() {
		if err := stocksRows.Scan(&stock); err != nil {
			log.Printf("Stocks Scan Error: %v", err)
			return 0, err
		}
	}
	return stock, nil
}

func updateStocks(productID string, updateStocks int, db *sql.DB) {
	updateStockQuery := fmt.Sprintf(updateRestockQuery, updateStocks, productID)
	log.Printf("[WORKER] update stock query : %v", updateStockQuery)
	_, err := db.Exec(updateStockQuery)
	if err != nil {
		log.Printf("restock UPDATE Query Error: %v | QUERY is: %v ", err, updateStockQuery)
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
