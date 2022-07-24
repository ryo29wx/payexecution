# Payexecution
This is Celery consumer(Worker)
Consume from the Broker built in Redis and flow to Backend(Redis)

Client ->  Broker(Redis) -> Consumer(Payexecution) -> Backend(Redis)

## Using Library
- [gocelery](https://github.com/gocelery/gocelery) <br>
- [stripe-go](https://pkg.go.dev/github.com/stripe/stripe-go)


## CircleCI
https://app.circleci.com/pipelines/github/ryo29wx/Purchase_Request_Consumer