package main

import (
	"fmt"
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
	insertSellerQuery = "INSERT INTO SELLER_INFO(seller_id, seller_name) VALUES ('test_sellerID', 'test_sellerName')"

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
	if status != nil {
		fmt.Println(status)
		t.Fail()
	}
}
