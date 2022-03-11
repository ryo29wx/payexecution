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

<<<<<<< HEAD
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

type job struct {
	Request  Requestparam
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
	sendReqLog(rp)

	// initialize redis controller struct
	HashSet(redisClient, rp.TransactionID, transactionFieldName, rp.Status)
=======
	celeryClient    *gocelery.CeleryClient
	notifyClient    *gocelery.CeleryClient
	redisClient     *redis.Client
	ctx             context.Context
	redisServerName string
)

func init() {
	prometheus.MustRegister(celeryReqs)
	redisServerName = os.Getenv("REDIS_SERVER")
	concurrency := 3
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
	cli.StartWorker()
	defer cli.StopWorker()
	fmt.Printf("[WORKER] worker start: concurrency=%v\n", concurrency)
	celeryClient = cli

	ctx = context.Background()
	//ctxLocal, cancel := context.WithTimeout(ctx, 5*time.Hour)
	//defer cancel()
	//ctx = ctxLocal
	pong, err := redisClient.Ping(ctx).Result()
	log.Println(pong, err)

	stripe.Key = secStgKey
}

func main() {
	// exec node-export service
	go exportMetrics()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4

	// Connect DB(MySQL)
	db, err := connectDB()
	if err != nil {
		logger.Error(" DB Connection ERROR!! in worker.go:", zap.Error(err))
		return 400
	}
	defer db.Close()

<<<<<<< HEAD
	nowStocks, err := getStocks(rp.ProductID, db)

	if rp.RestockFlag {
		logger.Debug("first restock route.")

		insertStock := nowStocks + int(rp.DealStock)
		updateStocks(rp.ProductID, insertStock, db)
		return 0
	}

	exclusion := SetNX(redisClient, rp.ProductID, "intrade")
	if !exclusion {
=======
	nowStocks, err := getStocks(productID, db)

	if restockFlag {
		log.Println("[WORKER] first restock route.")

		insertStock := nowStocks + dealStock
		updateStocks(productID, insertStock, db)
		return 0
	}

	exclusion := SetNX(redisClient, productID, "intrade")
	if exclusion {
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
		// debug
		logger.Debug("exclusion route.")

<<<<<<< HEAD
		if rp.RetryCnt > 10 {
			_, err = notifyClient.Delay(notifyTaskName, rp.Address, "The number of retries has been exceeded.　Please try again in a few minutes.")
=======
		if retryCnt > 10 {
			_, err = notifyClient.Delay(notifyTaskName, address, "The number of retries has been exceeded.　Please try again in a few minutes.")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
			if err != nil {
				logger.Error("FAILER notification failed:", zap.Error(err))
			}
<<<<<<< HEAD
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
=======
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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
			if st == "" {
				return 400
			}
			fallthrough

		case "succeeded":
<<<<<<< HEAD
			st, nowStocks = succeededTransaction(db, rp.TransactionID, rp.ProductID, rp.ImageURL, rp.ProductName, int(rp.Category), int(rp.DealStock), int(rp.Price), rp.RestockFlag)
=======
			st, nowStocks = succeededTransaction(db, transactionID, productID, imageURL, productName, category, dealStock, price, restockFlag)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
			if st == "" || nowStocks == -1 {
				return 400
			}
			fallthrough

		case "settlement_done":
<<<<<<< HEAD
			st = settleTransaction(rp.TransactionID, rp.Address, rp.ProductName)
=======
			st = settleTransaction(transactionID, address, productName)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
			if st == "" {
				return 400
			}
			fallthrough

		case "notification_done":
<<<<<<< HEAD
			notificationTransaction(rp.TransactionID, rp.ProductID)
=======
			notificationTransaction(transactionID, productID)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4

		default:
			logger.Debug("Do Nothing. :", zap.Any("request:", rp))
			return 400

		}

		// mockten mock to restock function
<<<<<<< HEAD
		nowStocks, err = getStocks(rp.ProductID, db)
		if nowStocks < 5 || err != nil {
			logger.Debug("Change restock flg is TRUE!!")
			rp.DealStock = 5
			rp.RestockFlag = true
			rp.Status = "start"
			_, err = celeryClient.Delay(taskName, rp)
=======
		nowStocks, err = getStocks(productID, db)
		if nowStocks < 5 || err != nil {
			log.Println("[WORKER] Change restock flg is TRUE!!")
			_, err = celeryClient.Delay(taskName, transactionID, productID, customerid, 5, totalAmount, imageURL, category, productName, price, userID, cardid, address, retryCnt, true, "start")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
		}

		countSends()
		return 0
	}

	return 0
}

func startTransaction(transactionID, userID, customerid, cardid, address string, totalAmount, retryCnt int) string {
<<<<<<< HEAD
	logger.Debug("startTransaction.", zap.String("tID", transactionID))
	var status string

	// if len(userID) == 0 {
		// TODO
		// BankAPIの使用(transfer_money)
	// } else {
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

	// }
	// Overwrite the result of payment completion to status
=======
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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
	HashSet(redisClient, transactionID, transactionFieldName, status)
	return status // succeeded
}

func succeededTransaction(db *sql.DB,
	transactionID, productID, imageURL, productName string,
	category, dealStock, price int,
	restockFlag bool) (string, int) {
<<<<<<< HEAD
	logger.Debug("[WORKER] succeeded route.")
=======
	log.Println("[WORKER] succeeded route.")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4

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
<<<<<<< HEAD
			logger.Debug("update stock route.", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", dealStock))
			insertStock := nowStocks - dealStock
			updateStocks(productID, insertStock, db)
		} else {
			logger.Debug("TooMuchDealStock...", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", dealStock))
=======
			log.Println("[WORKER] update stock route.")
			insertStock := nowStocks - dealStock
			updateStocks(productID, insertStock, db)
		} else {
			log.Println("[WORKER] The amount customer want to purchase is higher than the number of items in stock.")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
			return "", -1
		}
	} else {
		// Execute restock
		// debug
<<<<<<< HEAD
		logger.Debug("restock route.")
=======
		log.Println("[WORKER] restock route.")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4

		insertStock := nowStocks + dealStock
		updateStocks(productID, insertStock, db)
	}
	status := "settlement_done"
	HashSet(redisClient, transactionID, transactionFieldName, status)

	return status, nowStocks
}

func settleTransaction(transactionID, address, productName string) string {
<<<<<<< HEAD
	logger.Debug("settleTransaction.",
		zap.String("transactionID:", transactionID),
		zap.String("address:", address),
		zap.String("productName:", productName))

	_, err := notifyClient.Delay(notifyTaskName, address, fmt.Sprintf("The 'ProductName:[%v]' has been purchased.", productName))
	if err != nil {
		logger.Error("notification failed.")
=======
	log.Println("[WORKER] settlement done route.")

	_, err := notifyClient.Delay(notifyTaskName, address, fmt.Sprintf("The 'ProductName:[%v]' has been purchased.", productName))
	if err != nil {
		log.Printf("[WORKER] SUCCESS notification failed.")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
		return ""
	}

	status := "notification_done"
	HashSet(redisClient, transactionID, transactionFieldName, status)
	return status
}

func notificationTransaction(transactionID, productID string) {
<<<<<<< HEAD
	logger.Debug("notificationTransaction.",
		zap.String("transactionID:", transactionID),
		zap.String("productID:", productID))
=======
	log.Println("[WORKER] notification done route.")
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4

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
<<<<<<< HEAD
		logger.Error("requestPayment failed.:", zap.Error(err))
=======
		fmt.Printf("err: %s\n", err)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
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

<<<<<<< HEAD
func sendReqLog(rq *Requestparam) {
	log.Println(fmt.Printf("[WORKER] payexection request param %v", rq))
=======
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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
}

func timeToString(t time.Time) string {
	str := t.Format(layout)
	str = strings.Replace(str, "-", "", -1)
	return str
}

// HashSet :redis hash-set
// see : http://redis.shibu.jp/commandreference/hashes.html
func HashSet(redisClient *redis.Client, key, field string, value interface{}) {
<<<<<<< HEAD
	logger.Debug("HashSet.",
		zap.String("key:", key),
		zap.String("field:", field),
		zap.Any("value:", value))
=======
	log.Printf("[WORKER] redis.Client.HSet KEY: %v FIELD: %v VALUE: %v", key, field, value)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
	err := redisClient.HSet(ctx, key, field, value).Err()
	if err != nil {
		logger.Error("redis.Client.HSet Error:", zap.Error(err))
	}
}

// HashMSet :redis hash malti set
func HashMSet(redisClient *redis.Client, key, value string) {
	// Set
<<<<<<< HEAD
	logger.Debug("HashMSet.", zap.String("key:", key), zap.String("value:", value))
=======
	log.Printf("[WORKER] redis.Client.HMSet KEY: %v VALUE: %v", key, value)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
	err := redisClient.HMSet(ctx, key, value).Err()
	if err != nil {
		logger.Error("redis.Client.HMSet Error:", zap.Error(err))
	}
}

// HashGet :redis hash get
func HashGet(redisClient *redis.Client, key, field string) string {
	// Get
	// HGet(key, field string) *StringCmd
<<<<<<< HEAD
	logger.Debug("HashGet.", zap.String("key:", key), zap.String("field:", field))
=======

>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
	hgetVal, err := redisClient.HGet(ctx, key, field).Result()
	if err != nil {
		logger.Error("redis.Client.HGet Error:", zap.Error(err))
	}

	return hgetVal
}

// HashDelete : redis hash delete
func HashDelete(redisClient *redis.Client, key, field string) {
<<<<<<< HEAD
	logger.Debug("HashDelete.", zap.String("key:", key), zap.String("field:", field))
	err := redisClient.HDel(ctx, key, field).Err()
=======
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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
	if err != nil {
		logger.Error("redis.Client.HDel Error:", zap.Error(err))
	}
}

<<<<<<< HEAD
// Get : redis get
func Get(redisClient *redis.Client, key string) string {
	// Get
	logger.Debug("Get.", zap.String("key:", key))
	val, err := redisClient.Get(ctx, key).Result()
=======
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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
	if err != nil {
		logger.Error("redis.Client.Get Error:", zap.Error(err))
	}
<<<<<<< HEAD

	return val
}

// DELETE : redis delete
func DELETE(redisClient *redis.Client, key string) {
	logger.Debug("DELETE.", zap.String("key:", key))
	err := redisClient.Del(ctx, key).Err()
=======
}

// SetNX : redis setnx
func SetNX(redisClient *redis.Client, key, value string) bool {
	log.Printf("redis.Client.SetNX KEY: %v VALUE: %v", key, value)
	log.Println(ctx)

	res, err := redisClient.SetNX(ctx, key, value, 0).Result()
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
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

<<<<<<< HEAD
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
=======
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
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
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
<<<<<<< HEAD
	logger.Debug("getStocks.", zap.String("get stock query:", stockQuery))
	stocksRows, err := db.Query(stockQuery)
	if err != nil {
		logger.Error("SELECT stock Query Error: ",
			zap.Error(err),
			zap.String("Stock Query is:", stockQuery))
=======
	log.Printf("[WORKER] get stock query : %v", stockQuery)
	stocksRows, err := db.Query(stockQuery)
	if err != nil {
		log.Printf("[WORKER] SELECT stock Query Error: %v | Stock Query is: %v ", err, stockQuery)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
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
<<<<<<< HEAD
	logger.Debug("updateStocks.", zap.String("update stock query :", updateStockQuery))
	_, err := db.Exec(updateStockQuery)
	if err != nil {
		logger.Error("Stocks Scan Error: ",
			zap.Error(err),
			zap.String(" |Query is:", updateStockQuery))
=======
	log.Printf("[WORKER] update stock query : %v", updateStockQuery)
	_, err := db.Exec(updateStockQuery)
	if err != nil {
		log.Printf("restock UPDATE Query Error: %v | QUERY is: %v ", err, updateStockQuery)
>>>>>>> fe1d42d9c5748822262a03576d40ecfa5d1594f4
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
