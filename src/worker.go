package main

import (
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"log"

	"github.com/gocelery/gocelery"
	stripe "github.com/stripe/stripe-go"
	paymentintent "github.com/stripe/stripe-go/paymentintent"
)

const (
	PRODUCTION_MODE            = false
	PUBLISHABLE_KEY_STAGING    = "pk_test_j5fvxJmoN3TlTaNTgcATv0W000HRwOI317"
	PUBLISHABLE_KEY_PRODUCTION = "*******"
	SECRET_KEY_STAGING         = "sk_test_v4QrE3LoY9Cu2ki3IwQylABI00Hbes7WQT"
	SECRET_KEY_PRODUCTION      = "*******"
)

/*
Create a unique PaymentIntent in the order session.　　
This is for later retracing the purchase history, etc.
@see : https://stripe.com/docs/api/payment_intents
*/
func requestPayment(customerid string, deal_stock int, address string, retry_cnt int) (payid string) {
	payparams := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(deal_stock),
		Customer: stripe.String(customerid),
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		Receipt_email: stripe.String(address),
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
		if v.Type().Field(i).Name == "Status" {
			status = fmt.Sprintf("%v", v.Field(i).Interface())
			if status != "requires_payment_method" && retry_cnt > 0 {
				requestPayment(customerid, deal_stock, address, retry_cnt - 1)
			}
		}
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

func execute(product_id string, customerid string, deal_stock int, total_amount int, user_id string, cardid string, address string, retry_cnt int, restock_flag bool, status string) {
	sendReqLog(product_id, customerid, deal_stock, total_amount, user_id, cardid, address, retry_cnt, restock_flag, status)

	if status != "start" {
		log.Printf("[WORKER] Unable to execute.")
	}
	
	payid := requestPayment(customerid, deal_stock, address, retry_cnt)

	if payid == nil {
		log.Printf("[WORKER] payid is nil. customerid is [%v]." , customerid)
	}

	status = confirmPayment(cardid, payid)
	if status != "succeeded" {
		log.Printf("[WORKER] Authentication not successful.  payid:[%v] | customerid:[%v]." , payid, customerid)
	}
}

func sendReqLog(product_id string, customerid string, deal_stock int, total_amount int, user_id string, cardid string, address string, retry_cnt int, restock_flag bool, status string) {
	log.Printf("[WORKER] product_id:[%v] | customerid:[%v] | deal_stock:[%v] | total_amount:[%v] | user_id:[%v] | cardid:[%v] | address:[%v] | retry_cnt:[%v] | restock_flag:[%v]", product_id, customerid, deal_stock, total_amount, user_id, cardid, address, retry_cnt, restock_flag, status)
}

func main() {
	concurrency := 3
	stripe.Key = SECRET_KEY_STAGING
	cli, _ := gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker("redis://dev-redis.us-east1-b.c.go-portforio.internal:6379"),
		gocelery.NewRedisCeleryBackend("redis://dev-redis.us-east1-b.c.go-portforio.internal:6379"),
		concurrency,
	)

	cli.Register("worker.execute", execute)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	cli.StartWorker()
	defer cli.StopWorker()
	fmt.Printf("worker start: concurrency=%v\n", concurrency)

	select {
	case sig := <-c:
		fmt.Println("worker stopped by signal:", sig)
		return
	}
}