package main

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/ejbrever/trader/one/purchase"
	"github.com/shopspring/decimal"
)

var (
	fakePurchases   = []*purchase.Purchase{}
	fakePrice       = &fakeStockPrice{}
	fakeBuyOrderID  = 0
	fakeSellOrderID = 0
	fakeCash        = decimal.NewFromFloat(100000)
	stockHeldQty    = decimal.NewFromFloat(0)
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
	isBuyOrder := false
	for _, p := range c.purchases {
		if p.BuyOrder.ID == id {
			o = p.BuyOrder
			isBuyOrder = true
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
			// Use historical price data here. Also need logic to determine to take
			// limit or stop price (might also be random element to this value).
			filledAvgPrice := decimal.NewFromFloat(0)
			o.FilledAvgPrice = &filledAvgPrice
			totalPrice := o.FilledAvgPrice.Mul(o.Qty)
			//
			switch {
			case isBuyOrder:
				fakeCash = fakeCash.Sub(totalPrice)
				stockHeldQty = stockHeldQty.Add(o.Qty)
			default:
				fakeCash = fakeCash.Add(totalPrice)
				stockHeldQty = stockHeldQty.Sub(o.Qty)
			}
		}
	}
	return o
}

func (c *client) fakePlaceBuyOrder(req *alpaca.PlaceOrderRequest) {
	fakeBuyOrderID++
	c.purchases = append(c.purchases, &purchase.Purchase{
		BuyOrder: &alpaca.Order{
			ID:     fmt.Sprint(fakeBuyOrderID),
			Status: "new",
			Qty:    decimal.NewFromFloat(*purchaseQty),
			Side:   alpaca.Buy,
			Type:   alpaca.Market,
		},
	})
}

func (c *client) fakePlaceSellOrder(p *purchase.Purchase, req *alpaca.PlaceOrderRequest) {
	fakeSellOrderID++
	p.SellOrder = &alpaca.Order{
		ID:         fmt.Sprint(fakeSellOrderID),
		Status:     "new",
		LimitPrice: req.TakeProfit.LimitPrice,
		Qty:        decimal.NewFromFloat(*purchaseQty),
		Side:       alpaca.Sell,
		Legs: &[]alpaca.Order{{
			StopPrice:  req.StopLoss.StopPrice,
			LimitPrice: req.StopLoss.LimitPrice,
		}},
	}
}

func (c *client) fakeGetAccount() *alpaca.Account {
	return &alpaca.Account{
		Cash: fakeCash,
	}
}

func (c *client) fakeGetSymbolBars() []alpaca.Bar {
	// Get the last three historical 1 min bars.
	return nil
}
