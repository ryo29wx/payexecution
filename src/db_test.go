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
	insertQuery = "INSERT INTO PRODUCT_INFO(product_id, stock) VALUES ('test_productID', 5)"
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

	_, err1 := db.Exec(insertQuery)
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
