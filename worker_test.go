package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	stripe "github.com/stripe/stripe-go"
	"github.com/stripe/stripe-go/card"
	"github.com/stripe/stripe-go/customer"
	"github.com/stripe/stripe-go/token"
)

// mocking stripe
type mockStripeObj struct {
}

func (s *mockStripeObj) newParam() (*stripe.PaymentIntent, error) {
	p := new(stripe.PaymentIntent)
	p.ID = "pi_mockid"
	return p, fmt.Errorf("Error: %s", "mock error.")
}

func (s *mockStripeObj) confirm(payid string) (*stripe.PaymentIntent, error) {
	p := new(stripe.PaymentIntent)
	p.ID = payid
	p.Status = "succeeded"
	return p, fmt.Errorf("Error: %s", "mock error.")
}

func TestTimeToString(t *testing.T) {
	ti := time.Now()
	result := timeToString(ti)

	if result == "" {
		t.Fail()
	}

}

func TestRequestPayment(t *testing.T) {
	stripe.Key = "test_key"

	job := &job{stripeManager: &mockStripeObj{}}
	result := job.requestPayment()

	if !strings.Contains(result, "pi_") {
		fmt.Println(result)
		t.Fail()
	}

}

func getCutomerID(address string) (customerid string) {
	customparams := &stripe.CustomerParams{
		Email: stripe.String(address),
	}
	c, _ := customer.New(customparams)
	cv := reflect.ValueOf(*c)
	for i := 0; i < cv.NumField(); i++ {
		if cv.Type().Field(i).Name == "ID" {
			customerid = fmt.Sprintf("%v", cv.Field(i).Interface())
		}
	}
	return customerid

}

func TestConfirmPayment(t *testing.T) {
	testPayid := "pi_testid"
	job := &job{stripeManager: &mockStripeObj{}}
	result := job.confirmPayment(testPayid)

	if result != "succeeded" {
		fmt.Println(result)
		t.Fail()
	}

}

func TestSucceededTransaction(t *testing.T) {
	testTransactionID := "testTransactionID"
	testAddress := "testAddress"
	testProductName := "testProductName"

	// mock
	// db, mock := redismock.NewClientMock()
	// redisClient = db
	res := settleTransaction(testTransactionID, testAddress, testProductName)
	if res != "" {
		fmt.Println(res)
		t.Fail()
	}
}

func TestNotificationTransaction(t *testing.T) {
	testTransactionID := "testTransactionID"
	testProductID := "testProductID"

	// mock
	db, mock := redismock.NewClientMock()
	redisClient = db
	// transactionField := transactionFieldName
	// productField := "product"
	//tValue := "testTransactionValue"
	//pValue := "testProductValue"
	mock.ExpectDel(testTransactionID)
	mock.ExpectDel(testProductID)

	notificationTransaction(testTransactionID, testProductID)

}

func getCardToken(cardNum string, expMonth string, expYear string, cvc string, name string) (cardToken string) {
	tokenParams := &stripe.TokenParams{
		Card: &stripe.CardParams{
			Number:   stripe.String(cardNum),
			ExpMonth: stripe.String(expMonth),
			ExpYear:  stripe.String(expYear),
			CVC:      stripe.String(cvc),
			Name:     stripe.String(name),
		},
	}
	t, _ := token.New(tokenParams)
	token := reflect.ValueOf(*t)
	for i := 0; i < token.NumField(); i++ {
		if token.Type().Field(i).Name == "ID" {
			cardToken = fmt.Sprintf("%v", token.Field(i).Interface())
		}
	}
	return cardToken
}

func getCardID(customerid string, cardToken string) (cardid string) {
	cardparams := &stripe.CardParams{
		Customer: stripe.String(customerid),
		Token:    stripe.String(cardToken),
	}
	cardobj, _ := card.New(cardparams)
	cardv := reflect.ValueOf(*cardobj)
	for i := 0; i < cardv.NumField(); i++ {
		if cardv.Type().Field(i).Name == "ID" {
			cardid = fmt.Sprintf("%v", cardv.Field(i).Interface())
		}
	}
	return cardid
}

// redis mocking
func TestHashSet(t *testing.T) {
	db, mock := redismock.NewClientMock()

	key := "test_key"
	field := "test_field"
	value := "test_value"
	mock.ExpectHSet(key, field, value).RedisNil()
	HashSet(db, key, field, value)
}

func TestHashMSet(t *testing.T) {
	db, mock := redismock.NewClientMock()

	key := "test_key"
	value := "test_value"
	mock.ExpectMSet(key, value).RedisNil()
	HashMSet(db, key, value)
}

func TestHashGet(t *testing.T) {
	db, mock := redismock.NewClientMock()

	key := "test_key"
	value := "test_value"
	mock.ExpectHGet(key, value).SetVal("testResult")
	res := HashGet(db, key, value)
	fmt.Println(res)
}

func TestGet(t *testing.T) {
	db, mock := redismock.NewClientMock()

	key := "test_key"
	mock.ExpectGet(key).SetVal("testResult")
	res := Get(db, key)
	fmt.Println(res)
}

func TestZAdd(t *testing.T) {
	db, mock := redismock.NewClientMock()

	key := "test_key"
	z := &redis.Z{}
	mock.ExpectZAdd(key, z).RedisNil()
	ZAdd(db, key, z)
}
