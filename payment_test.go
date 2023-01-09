package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	stripe "github.com/stripe/stripe-go"
)

// Preparation for Unit Test
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

// Preparation for Unit Test
type mockRedisObj struct {
}

func (r *mockRedisObj) HashSet(redisClient *redis.Client, key, field string, value interface{}) {
	logger.Info("Called mock redis HashSet().")
}

func (r *mockRedisObj) HashMSet(redisClient *redis.Client, key, value string) {
	logger.Info("Called mock redis HashMSet().")
}

func (r *mockRedisObj) HashGet(redisClient *redis.Client, key, field string) string {
	logger.Info("Called mock redis HashGet().")

	return "TEST_HGET"
}

func (r *mockRedisObj) HashDelete(redisClient *redis.Client, key, field string) {
	logger.Info("Called mock redis HashDelete().")
}

func (r *mockRedisObj) Get(redisClient *redis.Client, key string) string {
	logger.Info("Called mock redis Get().")

	return "TEST_GET"
}

func (r *mockRedisObj) DELETE(redisClient *redis.Client, key string) {
	logger.Info("Called mock redis DELETE().")
}

func (r *mockRedisObj) ZAdd(redisClient *redis.Client, key string, z *redis.Z) {
	logger.Info("Called mock redis ZAdd().")
}

func (r *mockRedisObj) SetNX(redisClient *redis.Client, key, value string) bool {
	logger.Info("Called mock redis SetNX().")

	return true
}

// Preparation for Unit Test
type mockCeleryObj struct {
}

func (c *mockCeleryObj) delay(taskName string, rp *Requestparam) error {
	logger.Info("Called delay()")
	return nil
}

func (c *mockCeleryObj) nDelay(notifyTaskName string, rp *Requestparam) error {
	logger.Info("Called delay()")
	return nil
}

// @Test
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

func TestConfirmPayment(t *testing.T) {
	testPayid := "pi_testid"
	job := &job{stripeManager: &mockStripeObj{}}
	result := job.confirmPayment(testPayid)

	if result != "succeeded" {
		fmt.Println(result)
		t.Fail()
	}

}

func TestSettleTransaction(t *testing.T) {
	testTransactionID := "testTransactionID"
	testAddress := "testAddress"
	testProductName := "testProductName"
	testRp := &Requestparam{TransactionID: testTransactionID, Address: testAddress, ProductName: testProductName}

	pj = &paymentjob{redisManager: &mockRedisObj{}}
	celJob = &celeryJob{celeryManager: &mockCeleryObj{}}

	res := pj.settleTransaction(testRp)

	if res != "notification_done" {
		fmt.Println(res)
		t.Fail()
	}
}

func TestNotificationTransaction(t *testing.T) {
	testTransactionID := "testTransactionID"
	testProductID := "testProductID"
	testRp := &Requestparam{TransactionID: testTransactionID, ProductID: testProductID}

	pj = &paymentjob{redisManager: &mockRedisObj{}}
	pj.notificationTransaction(testRp)

}