FROM golang:1.14
WORKDIR /
COPY src .
RUN go get github.com/yabamuro/gocelery && \
    go get github.com/go-sql-driver/mysql && \
    go get github.com/go-redis/redis/v8 && \
    go get github.com/stripe/stripe-go && \
    go get github.com/stripe/stripe-go/paymentintent && \
    go get github.com/prometheus/client_golang/prometheus && \
    go get github.com/prometheus/client_golang/prometheus/promhttp && \
    go build worker.go
CMD ["/worker"]