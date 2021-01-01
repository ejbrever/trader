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
	backTestMinsToLookBack        = flag.Int("backtest_mins_to_look_back", 3, "The number of minutes to look back when doing historical analysis.")
	backtestStartTime             = flag.String("backtest_starttime", "", "The start time of the backtest in EST (format: 2006-01-02 15:04:00).")
	runBacktest                   = flag.Bool("run_backtest", false, "Run a backtest simulation.")
)

const (
	// historyReferenceTime is a string of the datetime layout in the historical files.
	referenceTime = "2006-01-02 15:04:05"

	// filled is the order of the status filled.
	filled = "filled"
)

var (
	fakePrice    = &fakeStockPrice{}
	fakeOrderID  = 0
	fakeCash     = decimal.NewFromFloat(100000)
	stockHeldQty = decimal.NewFromFloat(0)
)

type fakeStockPrice struct {
	badPrice decimal.Decimal
}

// newFake creates is a new() func for backtesting.
func newFake() (*client, error) {
	h, err := historicalData()
	if err != nil {
		return nil, fmt.Errorf("unable to read history: %v", err)
	}

	t, err := newFakeClock(*durationBetweenAction)
	if err != nil {
		return nil, err
	}

	c, err := new(*stockSymbol, *maxConcurrentPurchases)
	if err != nil {
		return nil, fmt.Errorf("unable to start backtesting trader-one: %v", err)
	}

	c.backtestHistory = h
	c.backtestClock = t

	return c, nil
}

func backtest() {
	// Seed rand.
	rand.Seed(time.Now().UnixNano())

	c, err := newFake()
	if err != nil {
		log.Printf(err.Error())
		return
	}
	log.Printf("backtest is beginning!")

	fmt.Printf("starting cash: %v\n", fakeCash.StringFixed(2))
	for c.backtestHistory.endTime.After(c.backtestClock.Now) {
		c.backtestClock.updateFakeClock()
		c.updateOrders()
		timeUntilMarketClose := c.backtestClock.TodaysCloseTime.Sub(c.backtestClock.Now)
		switch {
		case timeUntilMarketClose > 0*time.Second && timeUntilMarketClose < *timeBeforeMarketCloseToSell:
			// log.Printf("market is closing soon")
			c.closeOutTrading()
			c.backtestClock.Now = c.backtestClock.Now.Add(*timeBeforeMarketCloseToSell)
			continue
		case !c.backtestClock.IsOpen:
			// log.Printf("market is not open :(")
			continue
		default:
			// log.Printf("market is open!")
		}
		c.run(c.backtestClock.Now)
	}
	fmt.Printf("ending cash: %v\n", fakeCash.StringFixed(2))
}

func (c *client) endOfDayReport() {
	// TODO(ejbrever) Add change percentage for day trading.
	// TODO(ejbrever) Add change percentage for day for symbol.
	fmt.Printf("Time: %v\n", c.backtestClock.Now)
	fmt.Printf("Orders created: %v\n", fakeOrderID)
	fmt.Printf("Cash: %v\n\n", fakeCash.StringFixed(2))
}

// fakeCurrentPrice gets the historical ticker data for the current fake time.
func (c *client) fakeCurrentPrice() *historicalTickerData {
	t := timeToMinuteStart(c.backtestClock.Now)
	h, ok := c.backtestHistory.epochToTickerData[t.Unix()]
	if !ok {
		panic(fmt.Sprintf("unable to get historical data at %v", t))
	}
	return h
}

type history struct {
	// epochToTickerData is a map of epoch timestamps to the corresponding
	// historical ticker data.
	epochToTickerData map[int64]*historicalTickerData

	// endTime is the last time stored in the history.
	endTime time.Time
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
	var t time.Time
	for i < len(records) {
		infiniteLoopProtection++
		if infiniteLoopProtection > 117000 { // 390 mins/day * 300 days per year
			return nil, errors.New("infinite loop protection")
		}
		c.updateFakeClock()
		if !c.IsOpen {
			continue
		}
		for j := i; j < len(records); j++ {
			r := records[j]

			// Historical data files are in EST timezone.
			t, err = time.ParseInLocation(referenceTime, r[0], EST)
			if err != nil {
				return nil, fmt.Errorf("unable to read in time %q: %v", r[0], err)
			}
			if c.Now.After(t) {
				i++
				continue
			}
			if c.Now.Before(t) {
				h.epochToTickerData[c.Now.Unix()] = h.epochToTickerData[lastValidTimeStamp]
				i++
				break
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
	h.endTime = c.Now
	log.Printf("finished reading historical data, had %v rows", len(h.epochToTickerData))
	return h, nil
}

// randomFillOrder returns true or false randomly to inidicate if an order
// should be filled.
// This should return true 75% of the time.
func randomFillOrder() bool {
	return true
	// return rand.Intn(99) >= 24
}

type fakeClock struct {
	Now               time.Time
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
		TodaysCloseTime:   time.Date(t.Year(), t.Month(), t.Day(), 16, 0, 0, 0, EST),
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
	case c.Now.Before(c.TodaysOpenTime) || c.Now.After(c.TodaysCloseTime):
		c.IsOpen = false
		if c.Now.Hour() == 9 && c.Now.Minute() == 29 && c.Now.Second() == 0 {
			c.TodaysOpenTime = time.Date(c.Now.Year(), c.Now.Month(), c.Now.Day(), 9, 30, 0, 0, EST)
			c.TodaysCloseTime = time.Date(c.Now.Year(), c.Now.Month(), c.Now.Day(), 16, 0, 0, 0, EST)
		}
	default:
		c.IsOpen = true
	}
}

// fakeOrder is a func which is used for mocking the order() func during backtesting.
func (c *client) fakeOrder(id string) *alpaca.Order {
	var o *alpaca.Order
	for _, p := range c.purchases {
		if p.BuyOrder.ID == id {
			o = p.BuyOrder
			break
		}
		if p.SellOrder != nil && p.SellOrder.ID == id {
			o = p.SellOrder
			break
		}
	}

	if o == nil {
		panic(fmt.Sprintf("fakeOrder, could not find ID %v", id))
	}

	if o.Status != "new" {
		return o
	}

	switch {
	case o.Side == alpaca.Sell:
		c.fakeSellAttempt(o)
	case o.Side == alpaca.Buy:
		c.fakeBuyAttempt(o)
	default:
		panic(fmt.Sprintf("cannot have an order that is not a buy or sell: %+v", o))
	}
	return o
}

// fakeSellAttempt attempts to fill a sell order.
func (c *client) fakeSellAttempt(o *alpaca.Order) {
	if !randomFillOrder() {
		return
	}

	p := c.fakeCurrentPrice()
	legs := *o.Legs
	switch {
	case p.Close.GreaterThanOrEqual(*o.LimitPrice):
		o.Status = filled
		o.FilledQty = o.Qty
		o.FilledAvgPrice = &c.fakeCurrentPrice().Low

		fakeCash = fakeCash.Add(o.FilledAvgPrice.Mul(o.Qty))
		stockHeldQty = stockHeldQty.Sub(o.Qty)
	case p.Close.LessThanOrEqual(*legs[0].LimitPrice):
		// No need to do anything as the limit price was surpassed.
	case p.Close.LessThanOrEqual(*legs[0].StopPrice):
		o.Status = filled
		o.FilledQty = o.Qty
		o.FilledAvgPrice = &c.fakeCurrentPrice().Low

		fakeCash = fakeCash.Add(o.FilledAvgPrice.Mul(o.Qty))
		stockHeldQty = stockHeldQty.Sub(o.Qty)
	}
}

// fakeBuyAttempt attempts to fill a buy order.
func (c *client) fakeBuyAttempt(o *alpaca.Order) {
	if !randomFillOrder() {
		return
	}

	o.Status = filled
	o.FilledQty = o.Qty
	o.FilledAvgPrice = &c.fakeCurrentPrice().High

	fakeCash = fakeCash.Sub(o.FilledAvgPrice.Mul(o.Qty))
	stockHeldQty = stockHeldQty.Add(o.Qty)
}

func (c *client) fakePlaceBuyOrder(req *alpaca.PlaceOrderRequest) {
	fakeOrderID++
	c.purchases = append(c.purchases, &purchase.Purchase{
		BuyOrder: &alpaca.Order{
			CreatedAt: c.backtestClock.Now,
			ID:        fmt.Sprint(fakeOrderID),
			Status:    "new",
			Qty:       decimal.NewFromFloat(*purchaseQty),
			Side:      alpaca.Buy,
			Type:      alpaca.Market,
		},
	})
}

func (c *client) fakePlaceSellOrder(p *purchase.Purchase, req *alpaca.PlaceOrderRequest) {
	fakeOrderID++
	p.SellOrder = &alpaca.Order{
		ID:         fmt.Sprint(fakeOrderID),
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
	var bars []alpaca.Bar
	for i := *backTestMinsToLookBack; i > 0; i-- {
		h, ok := c.backtestHistory.epochToTickerData[timeToMinuteStart(c.backtestClock.Now).Unix()-int64(i*60)]
		if !ok {
			return nil
		}
		close, _ := h.Close.Float64()
		bars = append(bars, alpaca.Bar{
			Close: float32(close),
		})
	}
	return bars
}

func (c *client) fakeCloseOutTrading() {
	nowToMin := timeToMinuteStart(c.backtestClock.Now)
	h, ok := c.backtestHistory.epochToTickerData[nowToMin.Unix()]
	if !ok {
		panic(fmt.Sprintf("could not find data to close out @ %v", nowToMin))
	}
	// Sell at the lowest price since this is a market order.
	// Might need to take off even more to be realistic.
	fakeCash = fakeCash.Add(h.Low.Mul(stockHeldQty))

	c.endOfDayReport()

	// Zero out stock held and fake purchases.
	stockHeldQty = decimal.NewFromFloat(0)
	fakeOrderID = 0
	c.purchases = []*purchase.Purchase{}
}

// timeToMinuteStart returns the same time provided with the seconds and ns
// brought down to 0 which matches the historical data frequency.
func timeToMinuteStart(t time.Time) time.Time {
	return time.Date(
		t.Year(),
		t.Month(),
		t.Day(),
		t.Hour(),
		t.Minute(),
		0, // Second reset to 0.
		0, // Ns reset to 0.
		t.Location(),
	)
}
