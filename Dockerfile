FROM golang:1.14
WORKDIR /
COPY src .
RUN go get github.com/yabamuro/gocelery
RUN go get github.com/go-sql-driver/mysql
RUN go get github.com/go-redis/redis
RUN go get github.com/stripe/stripe-go
RUN go get github.com/stripe/stripe-go/paymentintent
RUN go get github.com/prometheus/client_golang/prometheus
RUN go get github.com/prometheus/client_golang/prometheus/promhttp
RUN go build worker.go
CMD ["/worker"]