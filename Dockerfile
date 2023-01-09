FROM golang:1.15.2 as builder
WORKDIR /
COPY src .
RUN go get github.com/yabamuro/gocelery && \
    go get github.com/go-sql-driver/mysql && \
    go get github.com/go-redis/redis/v8 && \
    go get github.com/stripe/stripe-go && \
    go get github.com/stripe/stripe-go/paymentintent && \
    go get github.com/prometheus/client_golang/prometheus && \
    go get github.com/prometheus/client_golang/prometheus/promauto && \
    go get github.com/prometheus/client_golang/prometheus/promhttp && \
    go get go.uber.org/zap && \
    go build .

# As runner
FROM alpine:3.11
RUN apk --no-cache add ca-certificates
RUN mkdir /lib64
RUN ln -s /lib/libc.musl-x86_64.so.1 /lib64/ld-linux-x86-64.so.2
COPY --from=builder  /worker .
ENTRYPOINT ["./Purchase_Request_Consumer", "-debug"]