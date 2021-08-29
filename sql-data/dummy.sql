DROP TABLE IF EXISTS `SELLER_INFO`;
DROP TABLE IF EXISTS `PRODUCT_INFO`;
DROP TABLE IF EXISTS `OPERATION_INFO`;

CREATE TABLE SELLER_INFO (
	seller_id CHAR(36) NOT NULL PRIMARY KEY,
	bc_address CHAR(34),
	register_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP ,
	seller_name VARCHAR(100) NOT NULL,
	stripe_pubkey CHAR(42),
	stripe_seckey CHAR(42),
	update_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP on update CURRENT_TIMESTAMP
) ;

CREATE TABLE PRODUCT_INFO (
    product_id CHAR(36) NOT NULL PRIMARY KEY,
    product_name VARCHAR(100) NOT NULL,
    seller_id CHAR(36) NOT NULL,
    stock INT NOT NULL,
    time TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP  ,
    update_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP on update CURRENT_TIMESTAMP,
    category INT NOT NULL,
    price INT NOT NULL,
    image_path VARCHAR(200),
    comment VARCHAR(500),
    CONSTRAINT seller_id 
    FOREIGN KEY (seller_id) 
    REFERENCES SELLER_INFO (seller_id)
    ON DELETE RESTRICT ON UPDATE RESTRICT
) ;
ALTER TABLE PRODUCT_INFO ADD INDEX index01(product_name);
ALTER TABLE PRODUCT_INFO ADD INDEX index02(seller_id);
ALTER TABLE PRODUCT_INFO ADD INDEX index03(category);

CREATE TABLE OPERATION_INFO (
    transaction_id CHAR(36) NOT NULL PRIMARY KEY,
    product_id CHAR(36) NOT NULL,
    audit_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    balance_info INT NOT NULL,
    deal_stock INT,
    ec_audit_info INT NOT NULL,
    CONSTRAINT product_id 
    FOREIGN KEY (product_id) 
    REFERENCES PRODUCT_INFO (product_id)
    ON DELETE RESTRICT ON UPDATE RESTRICT
);

INSERT INTO SELLER_INFO (seller_id, seller_name) 
VALUES (
	"bf53e077-bde1-4f7a-9f61-e1939bb1bc63",
	"test_seller"
);

INSERT INTO PRODUCT_INFO (product_id, product_name,seller_id,stock,category,price) 
VALUES (
    "350f48f8-ea53-442e-9cd9-82e968f3a4dd",
    "test",
    "bf53e077-bde1-4f7a-9f61-e1939bb1bc63",
    3,
    0,
    5
);