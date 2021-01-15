package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"log"
	"time"
	"context"

	_ "github.com/go-sql-driver/mysql"
	"github.com/go-redis/redis"
	//github.com/gocelery/gocelery"
	"github.com/yabamuro/gocelery"
	stripe "github.com/stripe/stripe-go"
	paymentintent "github.com/stripe/stripe-go/paymentintent"
)

const (
	PRODUCTION_MODE            = false
	TaskName                   = "worker.execute"
	NotifyTaskName             = "worker.notification"
	Queue                      = "payment"
	Notification               = "notification"
	PUBLISHABLE_KEY_STAGING    = "pk_test_j5fvxJmoN3TlTaNTgcATv0W000HRwOI317"
	PUBLISHABLE_KEY_PRODUCTION = "*******"
	SECRET_KEY_STAGING         = "sk_test_v4QrE3LoY9Cu2ki3IwQylABI00Hbes7WQT"
	SECRET_KEY_PRODUCTION      = "*******"
	UPDATE_RESTOCK_QUERY       = "UPDATE PRODUCT_INFO SET STOCK='%v' WHERE PRODUCT_ID='%v'"
	GET_STOCK_QUERY            = "SELECT STOCK FROM PRODUCT_INFO WHERE PRODUCT_ID='%v'"
	LAYOUT                     = "2006-01-02"
	TRANSACTION_ID             = "TransactionID"
)

var celery_client *gocelery.CeleryClient
var notify_client *gocelery.CeleryClient
var redis_client *redis.Client
var ctx context.Context

type Connect interface {
	ConnectDB() (*sql.DB, error)
}

type DB struct {}
/*
Create a unique PaymentIntent in the order session.　　
This is for later retracing the purchase history, etc.
@see : https://stripe.com/docs/api/payment_intents
*/
func requestPayment(customerid string, total_amount int, address string, retry_cnt int) (payid string) {
	payparams := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(int64(total_amount)),
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
		//	if status != "requires_payment_method" && retry_cnt > 0 {
		//		_, err = celery_client.Delay(TaskName, productID, customerid, dealStock, totalAmount, userID, cardid, address, retry_cnt+1, false, "start")
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

func execute(transaction_id string, product_id string, customerid string, deal_stock int, total_amount int, image_url string, category int, product_name string, price int, user_id string, cardid string, address string, retry_cnt int, restock_flag bool, status string) int {
	sendReqLog(transaction_id, product_id, customerid, deal_stock, total_amount, user_id, cardid, address, retry_cnt, restock_flag, status)
	HashSet(TRANSACTION_ID, transaction_id, status)

	// Connect DB(MySQL)
	d := DB{}
	db, err := d.ConnectDB()
	if err != nil {
		log.Printf("[WORKER] DB Connection ERROR!! in worker.go")
		return 400
	}
	defer db.Close()

	now_stocks, err := getStocks(product_id, db)

	if restock_flag {
		// Execute restock
		// debug
		log.Println("[WORKER] first restock route.")

		insert_stock := now_stocks + deal_stock
		updateStocks(product_id, insert_stock, db)
		return 0
	}

	exclusion := SetNX("ProductID", product_id)
	if exclusion {
		// debug
		log.Println("[WORKER] exclusion route.")

		if retry_cnt > 10 {
			_, err = notify_client.Delay(NotifyTaskName, address, "The number of retries has been exceeded.　Please try again in a few minutes.")
			if err != nil {
				log.Printf("[WORKER] FAILER notification failed.")
			}
			DELETE(transaction_id)
		}

		time.Sleep(10 * time.Second)
		_, err = celery_client.Delay(TaskName, transaction_id, product_id, customerid, deal_stock, total_amount, image_url, category, product_name, price, user_id, cardid, address, retry_cnt+1, false, "start")
		if err != nil {
			log.Printf("[WORKER] Enqueue Error:%v(ProductId:%v,TransactionId:%v,RetryCount:%v)", err, product_id, transaction_id, retry_cnt)
			return 400
		}
	} else {
		// st := Get(transaction_id)
		st := HashGet(TRANSACTION_ID, transaction_id)
		switch st {
			case "start":
				// debug
				log.Println("[WORKER] start route.")
				if len(user_id) == 0  {
					// TODO
					// BankAPIの使用(transfer_money)
				} else {
					// use strinp
					payid := requestPayment(customerid, total_amount, address, retry_cnt)
					if payid == "" || len(payid) <= 0 {
						log.Printf("[WORKER] payid is nil. customerid is [%v]." , customerid)
						return 400
					}
		
					status = confirmPayment(cardid, payid)
					if status != "succeeded" {
						log.Printf("[WORKER] Authentication not successful.  payid:[%v] | customerid:[%v]." , payid, customerid)
						return 400
					}

				}
				// Overwrite the result of payment completion to status

				// debug
				log.Println("[WORKER] start done.")
				HashSet(TRANSACTION_ID, transaction_id, status)
				fallthrough

			case "succeeded":
				// debug
				log.Println("[WORKER] succeeded route.")

				now_stocks, err = getStocks(product_id, db)

				if !restock_flag {
					// Usually purchased
					// update redis
					t := timeToString(time.Now())
					zadd_key := fmt.Sprintf("%v_%v", t, category)
					z := &redis.Z{}
					z.Score = float64(deal_stock)
					z.Member = product_id
					ZAdd(zadd_key, z)

					hsetValue := fmt.Sprintf("price:%v,image_url:%v,name:%v", price, image_url, product_name)
					HashSet("RANKING", product_id, hsetValue)

					if now_stocks >= deal_stock {
						insert_stock := now_stocks - deal_stock
						updateStocks(product_id, insert_stock, db)
					} else {
						log.Println("[WORKER] The amount customer want to purchase is higher than the number of items in stock.")
						return 400
					}
				} else {
					// Execute restock
					// debug
					log.Println("[WORKER] restock route.")

					insert_stock := now_stocks + deal_stock
					updateStocks(product_id, insert_stock, db)
				}
				status = "settlement_done"
				HashSet(TRANSACTION_ID, transaction_id, status)
				fallthrough

			case "settlement_done":
				// debug
				log.Println("[WORKER] settlement done route.")

				_, err = notify_client.Delay(NotifyTaskName, address, fmt.Sprintf("The 'ProductName:[%v]' has been purchased.", product_name))
				if err != nil {
					log.Printf("[WORKER] SUCCESS notification failed.")
				}

				status = "notification_done"
				HashSet(TRANSACTION_ID, transaction_id, status)
				fallthrough

			case "notification_done":
				// debug
				log.Println("[WORKER] notification done route.")

				// DELETE 
				DELETE("ProductID")
				DELETE(transaction_id)

			default:
				log.Println("[WORKER] Do Nothing...")
				return 400

		} 
		now_stocks, err = getStocks(product_id, db)
		if now_stocks < 5 || err != nil {
			log.Println("[WORKER] Change restock flg is TRUE!!")
			_, err = celery_client.Delay(TaskName, transaction_id, product_id, customerid, 5, total_amount, image_url, category, product_name, price, user_id, cardid, address, retry_cnt, true, "start")
		}

		return 0
	}

	return 0
}

func sendReqLog(transaction_id string, product_id string, customerid string, deal_stock int, total_amount int, user_id string, cardid string, address string, retry_cnt int, restock_flag bool, status string) {
	log.Println("[WORKER] transaction_id:[%v] | product_id:[%v] | customerid:[%v] | deal_stock:[%v] | total_amount:[%v] | user_id:[%v] | cardid:[%v] | address:[%v] | retry_cnt:[%v] | restock_flag:[%v] | status:[%v]", transaction_id, product_id, customerid, deal_stock, total_amount, user_id, cardid, address, retry_cnt, restock_flag, status)
}

func timeToString(t time.Time) string {
    str := t.Format(LAYOUT)
    return str
}

func HashSet(key string, field string, value string) {
	fmt.Println("redis.Client.HSet KEY: %v FIELD: %v VALUE: %v", key, field, value)
	err := redis_client.HSet(ctx, key, field, value).Err()
	if err != nil {
		fmt.Println("redis.Client.HSet Error:", err)
	}
}

func HashMSet(key string, value string) {
	// Set
	fmt.Println("redis.Client.HMSet KEY: %v VALUE: %v", key, value)
	err := redis_client.HMSet(ctx, key, value).Err()
	if err != nil {
		fmt.Println("redis.Client.HMSet Error:", err)
	}
}

func HashGet(key string, field string) string {
    // Get
	// HGet(key, field string) *StringCmd
	
    hgetVal, err := redis_client.HGet(ctx, key, field).Result()
    if err != nil {
        fmt.Println("redis.Client.HGet Error:", err)
    }
	fmt.Println(hgetVal)
	
	return hgetVal
}

func Get(key string) string {
	// Get
	fmt.Println("redis.Client.Get KEY: %v", key)
    val, err := redis_client.Get(ctx, key).Result()
    if err != nil {
        fmt.Println("redis.Client.Get Error:", err)
    }
	fmt.Println(val)
	
	return val
}

func ZAdd(key string, z *redis.Z)  {
	// Get
	fmt.Println("redis.Client.ZAdd KEY: %v", key)
    err := redis_client.ZAdd(ctx, key, z).Err()
    if err != nil {
        fmt.Println("redis.Client.ZAdd Error:", err)
    }
	
}

func SetNX(key string, value string) bool {
	res, err := redis_client.SetNX(ctx, key, value, 0).Result()
	if err != nil {
		fmt.Println("redis.Client.SetNX Error:", err)
	}
	
	return res
}

func DELETE(key string) {
	err := redis_client.Del(ctx, key).Err()
	if err != nil {
		fmt.Println("redis.Client.Del Error:", err)
	}
}


func HashDelete(key string, field string) {
	err := redis_client.HDel(ctx, key, field).Err()
	if err != nil {
		fmt.Println("redis.Client.HDel Error:", err)
	}
}

func getStocks(product_id string, db *sql.DB) (int, error) {
	stock_query := fmt.Sprintf(GET_STOCK_QUERY, product_id)
	stocksRows, err := db.Query(stock_query)
	if err != nil {
		log.Printf("SELECT stock Query Error: %v | Stock Query is: %v ", err, stock_query)
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

func updateStocks(product_id string, update_stocks int,  db *sql.DB) {
	update_stock_query := fmt.Sprintf(UPDATE_RESTOCK_QUERY, update_stocks, product_id)
	_, err := db.Exec(update_stock_query)
	if err != nil {
		log.Printf("restock UPDATE Query Error: %v | QUERY is: %v ", err, update_stock_query)
	}
}

// connect DB(mysql)
func (d DB) ConnectDB() (*sql.DB, error) {
	user := os.Getenv("SECRET_USER")
	pass := os.Getenv("SECRET_PASS")
	sdb := os.Getenv("SECRET_DB")
	table := os.Getenv("SECRET_TABLE")

	mySQLHost := fmt.Sprintf("%s:%s@tcp(%s)/%s", user, pass, sdb, table)
	db, err := sql.Open("mysql", mySQLHost)
	if err = db.Ping(); err != nil {
		log.Printf("[WORKER] db.Ping(): %s\n", err)
		return nil, err
	}

	return db, nil
}


func main() {
	concurrency := 3
	stripe.Key = SECRET_KEY_STAGING
	cli, _ := gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker("redis://redis.mockten.db.com:6379", Queue),
		gocelery.NewRedisCeleryBackend("redis://redis.mockten.db.com:6379"),
		concurrency,
	)

	notify_client, _ = gocelery.NewCeleryClient(
		gocelery.NewRedisCeleryBroker("redis://redis.mockten.db.com:6379", Notification),
		gocelery.NewRedisCeleryBackend("redis://redis.mockten.db.com:6379"),
		1,
	)

	redis_client = redis.NewClient(&redis.Options{
        Addr:     "redis.mockten.db.com:6379",
        Password: "", // no password set
        DB:       0,  // use default DB
	})

	cli.Register("worker.execute", execute)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	cli.StartWorker()
	defer cli.StopWorker()
	fmt.Printf("[WORKER] worker start: concurrency=%v\n", concurrency)

	celery_client = cli

	ctx = context.Background()
	ctx_local, cancel := context.WithTimeout(ctx, 5*time.Hour)
	defer cancel()
	ctx = ctx_local
	
	pong, err := redis_client.Ping(ctx).Result()
	log.Println(pong, err)

	select {
	case sig := <-c:
		fmt.Println("worker stopped by signal:", sig)
		return
	}
}