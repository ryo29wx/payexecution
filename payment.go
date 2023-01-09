package main

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	stripe "github.com/stripe/stripe-go"
	paymentintent "github.com/stripe/stripe-go/paymentintent"
	"go.uber.org/zap"
)

const (
	layout = "2006-01-02"
)

var (
	payexeReqCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "payexe_req_total",
		Help: "Total number of requests that have come to payexe",
	})

	payexeResCount = promauto.NewCounter(prometheus.CounterOpts{
		Name: "payexe_res_total",
		Help: "Total number of response that send from payexe",
	})
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

// Invoked from main function.
type job struct {
	stripeManager stripeManager
}

type paymentjob struct {
	redisManager redisManager
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

	payexeReqCount.Inc()

	// initialize redis controller struct
	pj.redisManager.HashSet(redisClient, rp.TransactionID, transactionFieldName, rp.Status)

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

	exclusion := pj.redisManager.SetNX(redisClient, rp.ProductID, "intrade")
	if !exclusion {

		// debug
		logger.Debug("exclusion route.")

		if rp.RetryCnt > 10 {
			err = celJob.celeryManager.nDelay(notifyTaskName, rp)

			if err != nil {
				logger.Error("FAILER notification failed:", zap.Error(err))
			}
			pj.redisManager.DELETE(redisClient, rp.ProductID)
		}

		time.Sleep(10 * time.Second)
		rp.RetryCnt = rp.RetryCnt + 1
		rp.RestockFlag = false
		rp.Status = "start"
		err = celJob.celeryManager.delay(taskName, rp)
		if err != nil {
			logger.Error("Enqueue Error:", zap.Error(err),
				zap.String("ProductId:", rp.ProductID),
				zap.String("TransactionID:", rp.TransactionID),
				zap.Int("RetryCount:", rp.RetryCnt))
			return 400
		}
	} else {
		st := pj.redisManager.HashGet(redisClient, rp.TransactionID, transactionFieldName)

		switch st {
		case "start":
			st = pj.startTransaction(rp)

			if st == "" {
				return 400
			}
			fallthrough

		case "succeeded":
			st, nowStocks = pj.succeededTransaction(db, rp)
			if st == "" || nowStocks == -1 {
				return 400
			}
			fallthrough

		case "settlement_done":
			st = pj.settleTransaction(rp)

			if st == "" {
				return 400
			}
			fallthrough

		case "notification_done":
			pj.notificationTransaction(rp)

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
			err = celJob.celeryManager.delay(taskName, rp)
			if err != nil {
				logger.Error("Celery task Error!")
			}
		}

		payexeResCount.Inc()
		return 0
	}

	return 0
}

func (pjob *paymentjob) startTransaction(rp *Requestparam) string {
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
	pjob.redisManager.HashSet(redisClient, rp.TransactionID, transactionFieldName, status)
	return status // succeeded
}

func (pjob *paymentjob) succeededTransaction(db *sql.DB, rp *Requestparam) (string, int) {
	logger.Debug("[WORKER] succeeded route.")

	nowStocks, err := getStocks(rp.ProductID, db)
	if err != nil {
		return "", -1
	}

	if !(rp.RestockFlag) {
		// Usually purchased
		// update redis
		t := timeToString(time.Now())
		zaddKey := fmt.Sprintf("%v_%v", t, rp.Category)
		zaddKey99 := fmt.Sprintf("%v_%v", t, "99")

		z := &redis.Z{}
		z.Score = float64(rp.DealStock)
		z.Member = rp.ProductID
		pjob.redisManager.ZAdd(redisClient, zaddKey, z)
		pjob.redisManager.ZAdd(redisClient, zaddKey99, z)

		// hsetValue := fmt.Sprintf("price:%v,imageURL:%v,name:%v", price, imageURL, productName)
		pjob.redisManager.HashSet(redisClient, rp.ProductID, "price", rp.Price)
		pjob.redisManager.HashSet(redisClient, rp.ProductID, "url", rp.ImageURL)
		pjob.redisManager.HashSet(redisClient, rp.ProductID, "name", rp.ProductName)

		if nowStocks >= int(rp.DealStock) {
			logger.Debug("update stock route.", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", int(rp.DealStock)))
			insertStock := nowStocks - int(rp.DealStock)
			updateStocks(rp.ProductID, insertStock, db)
		} else {
			logger.Debug("TooMuchDealStock...", zap.Int("nowStocks:", nowStocks), zap.Int("dealStock:", int(rp.DealStock)))

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
	pjob.redisManager.HashSet(redisClient, rp.TransactionID, transactionFieldName, status)

	return status, nowStocks
}

func (pjob *paymentjob) settleTransaction(rp *Requestparam) string {
	logger.Debug("settleTransaction.",
		zap.String("transactionID:", rp.TransactionID),
		zap.String("address:", rp.Address),
		zap.String("productName:", rp.ProductName))

	err := celJob.celeryManager.nDelay(notifyTaskName, rp)
	if err != nil {
		logger.Error("notification failed.")

		return ""
	}

	status := "notification_done"
	pjob.redisManager.HashSet(redisClient, rp.TransactionID, transactionFieldName, status)
	return status
}

func (pjob *paymentjob) notificationTransaction(rp *Requestparam) {
	logger.Debug("notificationTransaction.",
		zap.String("transactionID:", rp.TransactionID),
		zap.String("productID:", rp.ProductID))

	pjob.redisManager.DELETE(redisClient, rp.ProductID)
	pjob.redisManager.DELETE(redisClient, rp.TransactionID)
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

func timeToString(t time.Time) string {
	str := t.Format(layout)
	str = strings.Replace(str, "-", "", -1)
	return str
}
