package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/ejbrever/trader/one/purchase"
	"github.com/shopspring/decimal"
)

var (
	backtestFile                  = flag.String("backtest_file", "", "The filename with ticker data to use for backtesting.")
	backtestFileTimeBetweenAction = flag.Duration("backtest_file_duration_between_action", 60*time.Second, "The time granularity in the backtest file.")
	backtestStartTime             = flag.String("backtest_starttime", "", "The start time of the backtest in EST (format: 2006-01-02 15:04:00).")
	runBacktest                   = flag.Bool("run_backtest", false, "Run a backtest simulation.")
)

// historyReferenceTime is a string of the datetime layout in the historical files.
const referenceTime = "2006-01-02 15:04:05"

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

	_, err := historicalData()
	if err != nil {
		log.Printf("unable to read history: %v", err)
		return
	}
	if true {
		return
	}

	c, err := new(*stockSymbol, *maxConcurrentPurchases)
	if err != nil {
		log.Printf("unable to start backtesting trader-one: %v", err)
		return
	}
	fakePurchases = c.purchases
	log.Printf("backtest is beginning!")

	t, err := newFakeClock(*durationBetweenAction)
	if err != nil {
		log.Printf(err.Error())
		return
	}

	for {
		t.updateFakeClock()
		c.updateOrders()
		switch {
		case t.NextClose.Sub(t.Now) < *timeBeforeMarketCloseToSell:
			log.Printf("market is closing soon")
			trading = false
			c.closeOutTrading()
			time.Sleep(*timeBeforeMarketCloseToSell)
			continue
		case !t.IsOpen:
			trading = false
			log.Printf("market is not open :(")
			continue
		default:
			trading = true
			log.Printf("market is open!")
		}
		go c.run(t.Now)
	}
}

type history struct {
	// epochToTickerData is a map of epoch timestamps to the corresponding
	// historical ticker data.
	epochToTickerData map[int64]*historicalTickerData
}

func newHistory() *history {
	return &history{
		epochToTickerData: map[int64]*historicalTickerData{},
	}
}

type historicalTickerData struct {
	High  decimal.Decimal
	Low   decimal.Decimal
	Close decimal.Decimal
}

func historicalData() (*history, error) {
	log.Printf("starting to read historical data")
	f, err := os.Open(*backtestFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read backtest file: %v", err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}

	h := newHistory()
	c, err := newFakeClock(*backtestFileTimeBetweenAction)
	if err != nil {
		return nil, err
	}

	i := 0
	infiniteLoopProtection := 0
	var lastValidTimeStamp int64
	for i < len(records) {
		infiniteLoopProtection++
		if infiniteLoopProtection > 117000 { // 390 mins/day * 300 days per year
			return nil, errors.New("infinite loop protection")
		}
		c.updateFakeClock()
		if c.IsOpen {
			continue
		}
		for j := i; j < len(records); j++ {
			r := records[j]

			// Historical data files are in EST timezone.
			t, err := time.ParseInLocation(referenceTime, r[0], EST)
			if err != nil {
				return nil, fmt.Errorf("unable to read in time %q: %v", r[0], err)
			}
			if c.Now.Before(t) {
				h.epochToTickerData[c.Now.Unix()] = h.epochToTickerData[lastValidTimeStamp]
				break
			}
			if !c.Now.Equal(t) {
				return nil, fmt.Errorf("something went wrong, now: %v, test date: %v", c.Now, t)
			}

			// need to filter to only market open times.
			high, err := decimal.NewFromString(r[2])
			if err != nil {
				return nil, fmt.Errorf("unable to convert %q to float: %v", r[2], err)
			}
			low, err := decimal.NewFromString(r[3])
			if err != nil {
				return nil, fmt.Errorf("unable to convert %q to float: %v", r[3], err)
			}
			close, err := decimal.NewFromString(r[4])
			if err != nil {
				return nil, fmt.Errorf("unable to convert %q to float: %v", r[4], err)
			}
			h.epochToTickerData[t.Unix()] = &historicalTickerData{
				High:  high,
				Low:   low,
				Close: close,
			}
			lastValidTimeStamp = t.Unix()
			i++
			break
		}
	}
	log.Printf("finished reading historical data, had %v rows", len(h.epochToTickerData))
	return h, nil
}

// randomBool returns true or false randomly.
func randomBool() bool {
	return rand.Float32() < 0.5
}

type fakeClock struct {
	Now               time.Time
	NextClose         time.Time
	TodaysOpenTime    time.Time
	TodaysCloseTime   time.Time
	IsOpen            bool
	TimeBetweenAction time.Duration
}

func newFakeClock(timeBetweenAction time.Duration) (*fakeClock, error) {
	t, err := time.ParseInLocation(referenceTime, *backtestStartTime, EST)
	if err != nil {
		return nil, fmt.Errorf("unable to read in start time %q: %v", *backtestStartTime, err)
	}

	return &fakeClock{
		Now:               t.Add(-1 * timeBetweenAction), // Subtract one iteration to counteract first increase.
		TimeBetweenAction: timeBetweenAction,
		TodaysOpenTime:    time.Date(t.Year(), t.Month(), t.Day(), 9, 30, 0, 0, EST),
		TodaysCloseTime:   time.Date(t.Year(), t.Month(), t.Day(), 4, 0, 0, 0, EST),
	}, nil
}

// updateFakeClock increments the current time, determines if the market is
// open, and updates the days open market hours if needed.
// TODO(ejbrever) Account for days where market closes early.
func (c *fakeClock) updateFakeClock() {
	c.Now = c.Now.Add(c.TimeBetweenAction)
	switch {
	case c.Now.Weekday() == 0: // Sunday.
	case c.Now.Weekday() == 6: // Saturday.
	case c.Now.After(c.TodaysOpenTime) && c.Now.Before(c.TodaysCloseTime):
		c.IsOpen = true
	default:
		c.IsOpen = false
		if c.Now.Hour() == 1 && c.Now.Minute() == 0 && c.Now.Second() == 0 {
			c.TodaysOpenTime = time.Date(c.Now.Year(), c.Now.Month(), c.Now.Day(), 9, 30, 0, 0, EST)
			c.TodaysCloseTime = time.Date(c.Now.Year(), c.Now.Month(), c.Now.Day(), 4, 0, 0, 0, EST)
		}
	}
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

func (c *client) fakeCloseOutTrading() {
	// TODO(ejbrever) close out all trades for day.
}
