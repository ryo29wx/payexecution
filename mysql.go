package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"
	"go.uber.org/zap"

	stripe "github.com/stripe/stripe-go"
)

const (
	updateRestockQuery = "UPDATE PRODUCT_INFO SET STOCK='%v' WHERE PRODUCT_ID='%v'"
	getStockQuery      = "SELECT STOCK FROM PRODUCT_INFO WHERE PRODUCT_ID='%v'"
)

// Stripe Interface below
type sqlObj struct {
}

func newSQL(payparams *(stripe.PaymentIntentParams), cardid string) stripeManager {
	confirmation := &stripe.PaymentIntentConfirmParams{
		PaymentMethod: stripe.String(cardid),
	}
	s := &stripeObj{payparams, confirmation}
	return s
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
