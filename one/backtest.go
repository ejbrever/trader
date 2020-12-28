package main

import (
	"log"
	"math/rand"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/ejbrever/trader/one/purchase"
	"github.com/shopspring/decimal"
)

var (
	fakePurchases = []*purchase.Purchase{}
	fakePrice     = &fakeStockPrice{}
	fakeOrderID   = 0
)

type fakeStockPrice struct {
	badPrice decimal.Decimal
}

func backtest() {
	// Seed rand.
	rand.Seed(time.Now().UnixNano())

	c, err := new(*stockSymbol, *maxConcurrentPurchases)
	if err != nil {
		log.Printf("unable to start backtesting trader-one: %v", err)
		return
	}
	fakePurchases = c.purchases
	log.Printf("backtest is beginning!")

	// TODO(ejbrever) Get start time from backtesting data instead.
	t := time.Now()
	t.Add(-1 * *durationBetweenAction) // Subtract one iteration to counteract first increase.

	for {
		t.Add(*durationBetweenAction)
		// Need to account for days where market closes early.
		clock := getFakeClock()
		c.updateOrders()
		switch {
		case clock.NextClose.Sub(t) < *timeBeforeMarketCloseToSell:
			log.Printf("market is closing soon")
			trading = false
			c.closeOutTrading()
			time.Sleep(*timeBeforeMarketCloseToSell)
			continue
		case !clock.IsOpen:
			trading = false
			log.Printf("market is not open :(")
			continue
		default:
			trading = true
			log.Printf("market is open!")
		}
		go c.run(t)
	}
}

// randomBool returns true or false randomly.
func randomBool() bool {
	return rand.Float32() < 0.5
}

type fakeClock struct {
	NextClose time.Time
	IsOpen    bool
}

func getFakeClock() *fakeClock {
	return &fakeClock{}
}

// fakeOrder is a func which is used for mocking the order() func during backtesting.
func (c *client) fakeOrder(id string) *alpaca.Order {
	var o *alpaca.Order
	for _, p := range c.purchases {
		if p.BuyOrder.ID == id {
			o = p.BuyOrder
			break
		}
		if p.SellOrder.ID == id {
			o = p.SellOrder
			break
		}
	}
	if o.Status == "new" {
		if randomBool() {
			o.Status = "filled"
			o.FilledQty = o.Qty
			o.FilledAvgPrice = &fakePrice.badPrice
		}
	}
	return o
}

func (c *client) fakePlaceSellOrder(p *purchase.Purchase, req *alpaca.PlaceOrderRequest) {
	fakeOrderID++
	p.SellOrder = &alpaca.Order{
		// TODO(ejbrever) Add details below.
		// LimitPrice: nil,
		// StopPrice: nil,
		// Status: "",
		// ID:   string(fakeOrderID),
		Qty:  decimal.NewFromFloat(*purchaseQty),
		Side: alpaca.Sell,
	}
}
