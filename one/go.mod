module github.com/trader

go 1.15

replace github.com/alpacahq/alpaca-trade-api-go => /Users/ejbrever/go/src/github.com/alpacahq/alpaca-trade-api-go

require (
	github.com/alpacahq/alpaca-trade-api-go v1.7.0
	github.com/ejbrever/trader/one/database v0.0.0-20201226023622-b703b0666599
	github.com/ejbrever/trader/one/purchase v0.0.0-20201226023622-b703b0666599
	github.com/go-sql-driver/mysql v1.5.0
	github.com/shopspring/decimal v1.2.0
)
