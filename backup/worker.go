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
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	stripe "github.com/stripe/stripe-go"
	paymentintent "github.com/stripe/stripe-go/paymentintent"
	"github.com/yabamuro/gocelery"
	"go.uber.org/zap"
)

const (
	updateRestockQuery = "UPDATE PRODUCT_INFO SET STOCK='%v' WHERE PRODUCT_ID='%v'"
	getStockQuery      = "SELECT STOCK FROM PRODUCT_INFO WHERE PRODUCT_ID='%v'"
	layout             = "2006-01-02"
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

	payexeReqCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "payexe_req_total",
		Help: "Total number of requests that have come to payexe",
	})

	payexeResCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "payexe_res_total",
		Help: "Total number of response that send from payexe",
	})

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

	// Get Value from ConfigMap.
	taskName             = os.Getenv("taskName")
	notifyTaskName       = os.Getenv("notifyTaskName")
	queue                = os.Getenv("queue")
	notification         = os.Getenv("notification")
	publicStripeKey      = os.Getenv("pubKey")
	secretStripeKey      = os.Getenv("secKey")
	transactionFieldName = os.Getenv("transactionFieldName")
)

type Requestparam struct {
	TransactionID string
	ProductID     string
	Customerid    string
	DealStock     int32
	TotalAmount   int32
	ImageURL      string
	Category      int32
	ProductName   string
	Price         int32
	UserID        string
	Cardid        string
	Address       string
	RetryCnt      int
	RestockFlag   bool
	Status        string
}

func newRequestparam(
	transactionID, productID, customerid, imageURL, productName, userID, cardid, address, status string,
	dealStock, totalAmount, category, price int32, retryCnt int,
	restockFlag bool) *Requestparam {
	return &Requestparam{
		TransactionID: transactionID,
		ProductID:     productID,
		Customerid:    customerid,
		DealStock:     dealStock,
		TotalAmount:   totalAmount,
		ImageURL:      imageURL,
		Category:      category,
		ProductName:   productName,
		Price:         price,
		UserID:        userID,
		Cardid:        cardid,
		Address:       address,
		RetryCnt:      retryCnt,
		RestockFlag:   restockFlag,
		Status:        status}
}

// Stripe Interface below
type stripeObj struct {
	payparams    *(stripe.PaymentIntentParams)
	confirmparam *(stripe.PaymentIntentConfirmParams)
}

func newStripe(payparams *(stripe.PaymentIntentParams), cardid string) stripeManager {
	confirmation := &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(cardid),
	}
	s := &stripeObj{payparams, confirmation}
	return s
}

type stripeManager interface {
	newParam() (*stripe.PaymentIntent, error)
	confirm(string) (*stripe.PaymentIntent, error)
}

func (s *stripeObj) newParam() (*stripe.PaymentIntent, error) {
	obj, err := paymentintent.New(s.payparams)
	if err != nil {
		logger.Error("initialize Stripe API failed.")
	}

	return obj, err
}

func (s *stripeObj) confirm(payid string) (*stripe.PaymentIntent, error) {
	pi, err := paymentintent.Confirm(
		payid,
		s.confirmparam,
	)

	if err != nil {
		logger.Error("confirm payment to Stripe API failed.")
	}

	return pi, err
}

// Stripe Interface below
type sqlObj struct {
	m
}

func newSQL(payparams *(stripe.PaymentIntentParams), cardid string) stripeManager {
	confirmation := &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(cardid),
	}
	s := &stripeObj{payparams, confirmation}
	return s
}

// Invoked from main function.
type job struct {
	stripeManager stripeManager
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

}

func main() {
	verify()
	stripe.Key = secretStripeKey
	// exec node-export service
	go exportMetrics()

	// Celery INIT
	redisServerName := fmt.Sprintf("%s:%s", os.Getenv("REDIS_SERVICE_HOST"), os.Getenv("REDIS_SERVICE_PORT"))
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

func execute(transactionID, productID, customerid, imageURL, productName, userID, cardid, address, status string,
	dealStock, totalAmount, category, price, retryCnt float64,
	restockFlag bool) int {
	rp := newRequestparam(transactionID,
		productID,
		customerid,
		imageURL,
		productName,
		userID,
		cardid,
		address,
		status,
		int32(dealStock),
		int32(totalAmount),
		int32(category),
		int32(price),
		int(retryCnt),
		restockFlag)
	if rp == nil {
		logger.Error("RequestParam is nil")
	}

	return rp.pay()
}

func (rp *Requestparam) pay() int {

	countReqs()
	payexeReqCount.Inc()
	sendReqLog(rp)

	// initialize redis controller struct
	HashSet(redisClient, rp.TransactionID, transactionFieldName, rp.Status)

	// Connect DB(MySQL)
	db, err := connectDB()
	if err != nil {
		logger.Error(" DB Connection ERROR!! in worker.go:", zap.Error(err))
		return 400
	}
	defer db.Close()

	nowStocks, err := getStocks(rp.ProductID, db)

	if rp.RestockFlag {
		logger.Debug("first restock route.")

		insertStock := nowStocks + int(rp.DealStock)
		updateStocks(rp.ProductID, insertStock, db)
		return 0
	}

	exclusion := SetNX(redisClient, rp.ProductID, "intrade")
	if !exclusion {

		// debug
		logger.Debug("exclusion route.")

		if rp.RetryCnt > 10 {
			_, err = notifyClient.Delay(notifyTaskName, rp.Address, "The number of retries has been exceeded.　Please try again in a few minutes.")

			if err != nil {
				logger.Error("FAILER notification failed:", zap.Error(err))
			}
			DELETE(redisClient, rp.ProductID)
		}

		time.Sleep(10 * time.Second)
		rp.RetryCnt = rp.RetryCnt + 1
		rp.RestockFlag = false
		rp.Status = "start"
		_, err = celeryClient.Delay(taskName, rp)
		if err != nil {
			logger.Error("Enqueue Error:", zap.Error(err),
				zap.String("ProductId:", rp.ProductID),
				zap.String("TransactionID:", rp.TransactionID),
				zap.Int("RetryCount:", rp.RetryCnt))
			return 400
		}
	} else {
		st := HashGet(redisClient, rp.TransactionID, transactionFieldName)

		switch st {
		case "start":
			st = startTransaction(rp.TransactionID, rp.UserID, rp.Customerid, rp.Cardid, rp.Address, int(rp.TotalAmount), rp.RetryCnt)

			if st == "" {
				return 400
			}
			fallthrough

		case "succeeded":
			st, nowStocks = succeededTransaction(db, rp.TransactionID, rp.ProductID, rp.ImageURL, rp.ProductName, int(rp.Category), int(rp.DealStock), int(rp.Price), rp.RestockFlag)
			if st == "" || nowStocks == -1 {
				return 400
			}
			fallthrough

		case "settlement_done":
			st = settleTransaction(rp.TransactionID, rp.Address, rp.ProductName)

			if st == "" {
				return 400
			}
			fallthrough

		case "notification_done":
			notificationTransaction(rp.TransactionID, rp.ProductID)

		default:
			logger.Debug("Do Nothing. :", zap.Any("request:", rp))
			return 400

		}

		// mockten mock to restock function
		nowStocks, err = getStocks(rp.ProductID, db)
		if nowStocks < 5 || err != nil {
			logger.Debug("Change restock flg is TRUE!!")
			rp.DealStock = 5
			rp.RestockFlag = true
			rp.Status = "start"
			_, err = celeryClient.Delay(taskName, rp)

		}

		countSends()
		payexeResCount.Inc()
		return 0
	}

	return 0
}

func (rp *Requestparam) startTransaction(transactionID, userID, customerid, cardid, address string, totalAmount, retryCnt int) string {
	logger.Debug("startTransaction.", zap.String("tID", rp.TransactionID))
	var status string

	// if len(userID) == 0 {
	// TODO
	// BankAPIの使用(transfer_money)
	// } else {
	// use strinp
	payparams := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(int64(rp.TotalAmount) / 10),
		Customer: stripe.String(rp.Customerid),
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		// Receipt_email: stripe.String(address),
		PaymentMethodTypes: stripe.StringSlice([]string{
			"card",
		}),
	}

	st := newStripe(payparams, rp.Cardid)
	job := &job{stripeManager: st}

	payid := job.requestPayment()
	if payid == "" || len(payid) <= 0 {
		logger.Error("Payid is nil:", zap.String("Customerid:", rp.Customerid))
		return ""
	}

	status = job.confirmPayment(payid)
	if status != "succeeded" {
		logger.Error("Authentication not successful:",
			zap.String("Payid:", payid),
			zap.String("Customerid:", rp.Customerid))
		return ""
	}

	// Overwrite the result of payment completion to status
	HashSet(redisClient, rp.TransactionID, transactionFieldName, status)
	return status // succeeded
}

func (rp *Requestparam) succeededTransaction(db *sql.DB,
	transactionID, productID, imageURL, productName string,
	category, dealStock, price int,
	restockFlag bool) (string, int) {
	logger.Debug("[WORKER] succeeded route.")

	nowStocks, err := getStocks(rp.ProductID, db)
	if err != nil {
		return "", -1
	}

	if !restockFlag {
		// Usually purchased
		// update redis
		t := timeToString(time.Now())
		zaddKey := fmt.Sprintf("%v_%v", t, rp.Category)
		zaddKey99 := fmt.Sprintf("%v_%v", t, "99")

		z := &redis.Z{}
		z.Score = float64(rp.DealStock)
		z.Member = rp.ProductID
		ZAdd(redisClient, zaddKey, z)
		ZAdd(redisClient, zaddKey99, z)

		// hsetValue := fmt.Sprintf("price:%v,imageURL:%v,name:%v", price, imageURL, productName)
		HashSet(redisClient, rp.ProductID, "price", rp.Price)
		HashSet(redisClient, rp.ProductID, "url", rp.ImageURL)
		HashSet(redisClient, rp.ProductID, "name", rp.ProductName)

		if nowStocks >= int(rp.DealStock) {
			logger.Debug("update stock route.", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", rp.DealStock))
			insertStock := nowStocks - int(rp.DealStock)
			updateStocks(rp.ProductID, insertStock, db)
		} else {
			logger.Debug("TooMuchDealStock...", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", rp.DealStock))

			return "", -1
		}
	} else {
		// Execute restock
		// debug
		logger.Debug("restock route.")

		insertStock := nowStocks + int(rp.DealStock)
		updateStocks(rp.ProductID, insertStock, db)
	}
	status := "settlement_done"
	HashSet(redisClient, rp.TransactionID, transactionFieldName, status)

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
func (job *job) requestPayment() (payid string) {
	obj, err := job.stripeManager.newParam()
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
func (job *job) confirmPayment(payid string) (status string) {
	if payid == "" {
		logger.Error("Invoked with empty payid.")
		return "confirmation failed"
	}
	pi, _ := job.stripeManager.confirm(payid)

	v := reflect.ValueOf(*pi)
	for i := 0; i < v.NumField(); i++ {
		if v.Type().Field(i).Name == "Status" {
			status = fmt.Sprintf("%v", v.Field(i).Interface())
		}
	}
	return status
}

func sendReqLog(rq *Requestparam) {
	logger.Debug(fmt.Printf("[WORKER] payexection request param %v", rq))

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
	logger.Debug("SetNX.", zap.String("key:", key), zap.String("value:", value))
	res, err := redisClient.SetNX(ctx, key, value, time.Hour).Result()
	if err != nil {
		logger.Error("redis.Client.SetNX Error:", zap.Error(err))
	}
	fmt.Println("SetNX res : ", res)
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

// count
func countReqs() {
	celeryReqs.WithLabelValues("pay.execution", "accept").Add(1)
}

func countSends() {
	celerySends.WithLabelValues("pay.execution", "send").Add(1)
}
