package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"trader/purchase"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/common"
	"github.com/shopspring/decimal"
)

type client struct {
	stockSymbol      string
	allowedPurchases int
	purchases        []*purchase.Purchase
	alpacaClient     *alpaca.Client
}

func new(stockSymbol string, allowedPurchases int) *client {
	return &client{
		allowedPurchases: allowedPurchases,
		alpacaClient:     alpaca.NewClient(common.Credentials()),
		stockSymbol:      stockSymbol,
	}
}

// boughtNotSold returns a slice of purchases that have been bought and not been
// sold.
func (c *client) boughtNotSold() []*purchase.Purchase {
	var notSold []*purchase.Purchase
	for _, p := range c.purchases {
		if p.BuyOrder == nil || p.BuyOrder.FilledAt == nil {
			continue
		}
		if p.SellOrder == nil || p.SellOrder.FilledAt == nil {
			notSold = append(notSold, p)
		}
	}
	return notSold
}

// inProgressBuyOrders returns a slice of all buy purchases which are still
// open and in progress.
func (c *client) inProgressBuyOrders() []*purchase.Purchase {
	var inProgress []*purchase.Purchase
	for _, p := range c.purchases {
		if !p.InProgressBuyOrder() {
			continue
		}
		inProgress = append(inProgress, p)
	}
	return inProgress
}

// unfulfilledSellOrders returns a slice of all sell purchases which are still
// open and in progress.
func (c *client) inProgressSellOrders() []*purchase.Purchase {
	var inProgress []*purchase.Purchase
	for _, p := range c.purchases {
		if !p.InProgressSellOrder() {
			continue
		}
		inProgress = append(inProgress, p)
	}
	return inProgress
}

func (c *client) run(t time.Time) {
	if err := c.updateOrders(); err != nil {
		log.Printf("updateOrders @ %v: %v\n", t, err)
		return
	}
	c.buy(t)
	c.sell(t)
}

func (c *client) updateOrders() error {
	var err error
	for _, o := range c.inProgressBuyOrders() {
		id := o.BuyOrder.ID
		o.BuyOrder, err = c.alpacaClient.GetOrder(id)
		if err != nil {
			return fmt.Errorf("GetOrder %q error: %v", id, err)
		}
	}
	for _, o := range c.inProgressSellOrders() {
		id := o.SellOrder.ID
		o.SellOrder, err = c.alpacaClient.GetOrder(id)
		if err != nil {
			return fmt.Errorf("GetOrder %q error: %v", id, err)
		}
	}
	return nil
}

// Sell side:
// If current price greater than buy price, then sell.
func (c *client) sell(t time.Time) {
	boughtNotSold := c.boughtNotSold()
	if len(boughtNotSold) == 0 {
		return
	}
	q, err := c.alpacaClient.GetLastQuote(c.stockSymbol)
	if err != nil {
		log.Printf("unable to get last quote @ %v: %v\n", t, err)
		return
	}
	for _, p := range boughtNotSold {
		if p.BuyFilledAvgPriceFloat() <= q.Last.AskPrice {
			continue
		}
		c.placeSellOrder(t, p)
	}
}

func (c *client) placeSellOrder(t time.Time, p *purchase.Purchase) {
	var err error
	p.SellOrder, err = c.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		AccountID:   "",
		AssetKey:    &c.stockSymbol,
		Qty:         decimal.NewFromFloat(1),
		Side:        alpaca.Sell,
		Type:        alpaca.Market,
		TimeInForce: alpaca.Day,
	})
	if err != nil {
		log.Printf("unable to place sell order @ %v: %v\n", t, err)
		return
	}
	log.Printf("sell order placed @ %v:\n%+v\n", t, p.SellOrder)
}

// Buy side:
// Look at most recent two 1sec Bars.
// If positive direction, buy.
func (c *client) buy(t time.Time) {
	if len(c.boughtNotSold()) >= c.allowedPurchases {
		log.Printf("allowable purchases used @ %v\n", t)
		return
	}
	if !c.buyEvent(t) {
		return
	}
	c.placeBuyOrder(t)
}

// buyEvent determines if this time is a buy event.
func (c *client) buyEvent(t time.Time) bool {
	limit := 2
	startDt := time.Now()
	endDt := startDt.Add(-5 * time.Second)
	bars, err := c.alpacaClient.GetSymbolBars(c.stockSymbol, alpaca.ListBarParams{
		Timeframe: "1Min",
		StartDt:   &startDt,
		EndDt:     &endDt,
		Limit:     &limit,
	})
	if err != nil {
		log.Printf("GetSymbolBars err @ %v: %v\n", t, err)
		return false
	}
	if len(bars) < 2 {
		log.Printf("did not return at least two bars, so cannot proceed @ %v\n", t)
		return false
	}
	if bars[len(bars)-1].Close <= bars[len(bars)-2].Close {
		log.Printf("non-positive improvement of $%v => $%v @ %v\n",
			bars[len(bars)-2].Close, bars[len(bars)-1].Close, t)
		return false
	}
	return true
}

func (c *client) placeBuyOrder(t time.Time) {
	o, err := c.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		AccountID:     "",
		AssetKey:      &c.stockSymbol,
		Qty:           decimal.NewFromFloat(1),
		Side:          alpaca.Buy,
		Type:          alpaca.Market,
		TimeInForce:   alpaca.Day,
		ClientOrderID: fmt.Sprintf("one:%v", t),
	})
	if err != nil {
		log.Printf("unable to place buy order @ %v: %v\n", t, err)
		return
	}
	c.purchases = append(c.purchases, &purchase.Purchase{
		BuyOrder: o,
	})
	log.Printf("buy order placed @ %v:\n%+v\n", t, o)
}

// close closes out all trading for the day.
func (c *client) close() {
	if err := c.alpacaClient.CancelAllOrders(); err != nil {
		log.Printf("unable to cancel all orders: %v\n", err)
	}
	if err := c.alpacaClient.CloseAllPositions(); err != nil {
		log.Printf("unable to close all positions: %v\n", err)
	}
	log.Printf("My hour of trading is over!")
}

func main() {
	stockSymbol := "SPY"
	allowedPurchases := 20

	c := new(stockSymbol, allowedPurchases)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	done := make(chan bool)
	go func() {
		time.Sleep(20 * time.Minute)
		done <- true
	}()
	for {
		log.Printf("another 30 seconds...\n")
		select {
		case <-done:
			c.close()
			return
		case t := <-ticker.C:
			go c.run(t)
		}
	}
}

func init() {
	os.Setenv(common.EnvApiKeyID, "PKMYQANTSQ1QRQW9FSO6")
	os.Setenv(common.EnvApiSecretKey, "d5T9VG79siGgofz8snYZDX85wLnVQHtPDQfvRMET")

	log.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

	alpaca.SetBaseUrl("https://paper-api.alpaca.markets")
}
