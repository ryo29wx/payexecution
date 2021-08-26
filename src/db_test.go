package main

import (
	"fmt"
	"log"
	"testing"
)

const (
	query = "CREATE TABLE `PRODUCT_INFO` (" +
		"`product_id` char(36) NOT NULL," +
		"`product_name` varchar(100) NOT NULL," +
		"`seller_id` char(36) NOT NULL," +
		"`stock` int(11) NOT NULL," +
		"`time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP," +
		"`update_time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP," +
		"`category` int(11) NOT NULL," +
		"`price` int(11) NOT NULL," +
		"`rate` int(11) NOT NULL," +
		"`image_path` varchar(200) DEFAULT NULL," +
		"`comment` varchar(500) DEFAULT NULL," +
		"PRIMARY KEY (`product_id`)," +
		"KEY `index01` (`product_name`)," +
		"KEY `index02` (`seller_id`)," +
		"KEY `index03` (`category`)," +
		"CONSTRAINT `seller_id` FOREIGN KEY (`seller_id`) REFERENCES `SELLER_INFO` (`seller_id`)" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8;"
	insertProductQuery = "INSERT INTO PRODUCT_INFO(product_id, product_name, seller_id, stock, category, price) VALUES ('test_productID', 'test_productName', 'test_sellerID', 5, 1, 100)"
	insertSellerQuery  = "INSERT INTO SELLER_INFO(seller_id, seller_name) VALUES ('test_sellerID', 'test_sellerName')"
)

func init() {
	db, err := connectDB()
	if err != nil {
		panic(err)
	}
	db.Exec(query)
}

func TestUpdateStocks_DB(t *testing.T) {
	db, err := connectDB()
	if err != nil {
		fmt.Println(err)
		t.Fail()
	}
	_, err2 := db.Exec(insertSellerQuery)
	if err2 != nil {
		fmt.Println(err2)
		t.Fail()
	}

	_, err1 := db.Exec(insertProductQuery)
	if err1 != nil {
		fmt.Println(err1)
		t.Fail()
	}

	res, err := getStocks("test_productID", db)
	if res != 5 || err != nil {
		fmt.Println(res, err)
		t.Fail()
	}

	updateStocks("test_productID", 10, db)
	res, err = getStocks("test_productID", db)
	if res != 10 || err != nil {
		fmt.Println(res, err)
		t.Fail()
	}
}

func TestSettleTransaction_DB(t *testing.T) {
	transactionID := "test_transactionID"
	address := "test_address"
	productName := "test_productName"

	status := settleTransaction(transactionID, address, productName)
	if status != "" {
		fmt.Println(status)
		t.Fail()
	}
}

func TestNotificationTransaction_DB(t *testing.T) {
	transactionID := "test_transactionID"
	productID := "test_productID"

	res := SetNX(redisClient, transactionID, "intrade")
	if res == false {
		log.Println(res)
		t.Fail()
	}
	res = SetNX(redisClient, productID, "intrade")
	if res == false {
		log.Println(res)
		t.Fail()
	}
	value := Get(redisClient, productID)

	if value != "intrade" {
		fmt.Println(value)
		t.Fail()
	}
	value = Get(redisClient, transactionID)
	if value != "intrade" {
		fmt.Println(value)
		t.Fail()
	}

	notificationTransaction(transactionID, productID)

	value = Get(redisClient, productID)
	if value != "" {
		fmt.Println(value)
		t.Fail()
	}
	value = Get(redisClient, transactionID)
	if value != "" {
		fmt.Println(value)
		t.Fail()
	}

}

func TestSucceededTransaction_DB(t *testing.T) {
	transactionID := "test_transactionID"
	productID := "test_productID"
	imageURL := "test_imageURL"
	productName := "test_productName"
	category := 1
	dealStock := 2
	price := 300
	restockFlag := false

	db, _ := connectDB()
	res1, res2 := succeededTransaction(db, transactionID, productID, imageURL, productName, category, dealStock, price, restockFlag)
	if res1 != "settlement_done" {
		log.Println("error from succeededTransaction status")
		log.Println(res1)
		t.Fail()
	} else if res2 != 10 {
		log.Println("error from succeededTransaction newStock")
		log.Println(res2)
		t.Fail()
	}

	if HashGet(redisClient, productID, "price") != "300" {
		log.Println("Error HashGet Price")
		t.Fail()
	} else if HashGet(redisClient, productID, "url") != imageURL {
		log.Println("Error HashGet URL")
		t.Fail()
	} else if HashGet(redisClient, productID, "name") != productName {
		log.Println("Error HashGet ProductName")
		t.Fail()
	}
}

func TestSucceededTransaction_getStockErr_DB(t *testing.T) {
	transactionID := "test_transactionID"
	productID := "test_productID_DoNoting"
	imageURL := "test_iamgeURL"
	productName := "test_productName"
	category := 1
	dealStock := 2
	price := 300
	restockFlag := false

	db, _ := connectDB()
	res1, res2 := succeededTransaction(db, transactionID, productID, imageURL, productName, category, dealStock, price, restockFlag)
	if res1 != "" {
		log.Println("error from succeededTransaction status")
		log.Println(res1)
		t.Fail()
	} else if res2 != -1 {
		log.Println("error from succeededTransaction newStock")
		log.Println(res2)
		t.Fail()
	}
}

func TestSucceededTransaction_canotBuy_DB(t *testing.T) {
	transactionID := "test_transactionID"
	productID := "test_productID_DoNoting"
	imageURL := "test_iamgeURL"
	productName := "test_productName"
	category := 1
	dealStock := 100 // too large
	price := 300
	restockFlag := false

	db, _ := connectDB()
	res1, res2 := succeededTransaction(db, transactionID, productID, imageURL, productName, category, dealStock, price, restockFlag)
	if res1 != "" {
		log.Println("error from succeededTransaction status")
		log.Println(res1)
		t.Fail()
	} else if res2 != -1 {
		log.Println("error from succeededTransaction newStock")
		log.Println(res2)
		t.Fail()
	}

	// hashset will be executed
	if HashGet(redisClient, productID, "price") != "300" {
		log.Println("Error HashGet Price")
		t.Fail()
	} else if HashGet(redisClient, productID, "url") != imageURL {
		log.Println("Error HashGet URL")
		t.Fail()
	} else if HashGet(redisClient, productID, "name") != productName {
		log.Println("Error HashGet ProductName")
		t.Fail()
	}
}

func TestSucceededTransaction_restorck_DB(t *testing.T) {
	transactionID := "test_transactionID"
	productID := "test_productID_DoNoting"
	imageURL := "test_iamgeURL"
	productName := "test_productName"
	category := 1
	dealStock := 2
	price := 300
	restockFlag := true

	db, _ := connectDB()
	res1, res2 := succeededTransaction(db, transactionID, productID, imageURL, productName, category, dealStock, price, restockFlag)
	if res1 != "settlement_done" {
		log.Println("error from succeededTransaction status")
		log.Println(res1)
		t.Fail()
	} else if res2 != 0 {
		log.Println("error from succeededTransaction newStock")
		log.Println(res2)
		t.Fail()
	}

	// hashset will be executed
	if HashGet(redisClient, productID, "price") != "300" {
		log.Println("Error HashGet Price")
		t.Fail()
	} else if HashGet(redisClient, productID, "url") != imageURL {
		log.Println("Error HashGet URL")
		t.Fail()
	} else if HashGet(redisClient, productID, "name") != productName {
		log.Println("Error HashGet ProductName")
		t.Fail()
	}
}

func TestStartTransaction_DB(t *testing.T) {
	transactionID := "test_transactionID"
	userID := "test_userID"
	cardToken := getCardToken("4242424242424242", "7", "2025", "123", "testUser")
	customerid := getCutomerID("test@gmail.com")

	cardid := getCardID(customerid, cardToken)
	address := "test_address"
	totalAmount := 100
	retryCnt := 3

	res1 := startTransaction(transactionID, userID, customerid, cardid, address, totalAmount, retryCnt)
	if res1 != "succeeded" {
		log.Println("error from startTransaction status")
		log.Println(res1)
		t.Fail()
	}

	// hashset will be executed
	if HashGet(redisClient, transactionID, transactionFieldName) != "succeeded" {
		log.Println("Error HashGet Price")
		t.Fail()
	}
}
