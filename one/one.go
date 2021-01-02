package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/common"
	"github.com/ejbrever/trader/one/database"
	"github.com/ejbrever/trader/one/purchase"
	"github.com/shopspring/decimal"
)

var (
	apiEndpoint                 = flag.String("api_endpoint", "https://paper-api.alpaca.markets", "The REST API endpoint for Alpaca.")
	apiKeyID                    = flag.String("api_key_id", "", "The Alpaca API Key ID.")
	apiSecretKey                = flag.String("api_secret_key", "", "The Alpaca API Secret Key.")
	durationBetweenAction       = flag.Duration("duration_between_action", 30*time.Second, "The time between each attempt to buy or sell.")
	durationToRun               = flag.Duration("duration_to_run", 10*time.Second, "The time that the job should run.")
	maxConcurrentPurchases      = flag.Int("max_concurrent_purchases", 0, "The maximum number of allowed purchases at a given time.")
	purchaseQty                 = flag.Float64("purchase_quanity", 0, "Quantity of shares to purchase with each buy order.")
	stockSymbol                 = flag.String("stock_symbol", "", "The stock to buy an sell.")
	timeBeforeMarketCloseToSell = flag.Duration("time_before_market_close_to_sell", 1*time.Hour, "The time before market close that all positions should be closed out.")
	numHistoricalBarsToUse      = flag.Int("num_historical_bars_to_use", 3, "The number of historical bars to request when determining if now is a buy event.")
	allSequentialIncreasesToBuy = flag.Bool("all_sequential_increases_to_buy", false, "If true, all historical bars must increase sequentially to initiate a buy event.")
	minSlopeRequiredToBuy       = flag.Float64("min_slope_required_to_buy", 1.3, "The minumun slope of the trend line required to initiate a buy event.")
)

var (
	// EST is the timezone for Eastern time.
	EST *time.Location

	// PST is the timezone for Pacific time.
	PST *time.Location

	// Is trading currently allowed by the algorithm?
	trading bool
)

type client struct {
	concurrentPurchases int
	alpacaClient        *alpaca.Client
	dbClient            database.Client // This is an interface.
	purchases           []*purchase.Purchase
	stockSymbol         string

	// The following struct items are relevant when running backtests.
	backtestHistory          *history
	backtestClock            *fakeClock
	backtestOrderID          int
	backtestStockHeldQty     decimal.Decimal
	backtestCash             decimal.Decimal
	backtestCashStart        decimal.Decimal
	backtestCashStartOfDay   decimal.Decimal
	backtestSymbolEndOfDay   decimal.Decimal
	backtestSymbolStartOfDay decimal.Decimal
}

func new(stockSymbol string, concurrentPurchases int) (*client, error) {
	var purchases []*purchase.Purchase
	var alpacaClient *alpaca.Client
	var db database.Client
	var err error
	switch {
	case *runBacktest:
		db, _ = database.NewFake()
	default:
		alpacaClient = alpaca.NewClient(common.Credentials())
		db, err = database.New()
		if err != nil {
			return nil, fmt.Errorf("unable to open db: %v", err)
		}
		purchases, err = db.Purchases(time.Now().In(PST).YearDay(), PST)
		if err != nil {
			return nil, fmt.Errorf("unable to get all purchases: %v", err)
		}
	}
	return &client{
		concurrentPurchases: concurrentPurchases,
		alpacaClient:        alpacaClient,
		dbClient:            db,
		purchases:           purchases,
		stockSymbol:         stockSymbol,
	}, nil
}

// boughtNotSelling returns a slice of purchases that have been bought and
// and a sell order is not placed.
func (c *client) boughtNotSelling() []*purchase.Purchase {
	var notSelling []*purchase.Purchase
	for _, p := range c.purchases {
		if !p.BuyFilled() {
			continue
		}
		if p.NotSelling() {
			notSelling = append(notSelling, p)
		}
	}
	return notSelling
}

// inProgressPurchases returns a slice of purchases where the buy is at any
// valid stage (in progress or filled) and has not been entirely sold.
func (c *client) inProgressPurchases() []*purchase.Purchase {
	var inProgress []*purchase.Purchase
	for _, p := range c.purchases {
		switch {
		case p.SellFilled():
			continue
		case p.BuyInProgress() || p.SellInProgress():
			inProgress = append(inProgress, p)
		case p.BuyHasStatus("replaced") || p.SellHasStatus("replaced"):
			inProgress = append(inProgress, p)
		}
	}
	return inProgress
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
	c.cancelOutdatedOrders()
	c.buy(t)
	c.sell()
}

// cancelOutdatedOrders cancels all buy orders that have been outstanding for
// more than 5 mins.
func (c *client) cancelOutdatedOrders() {
	now := time.Now()
	if *runBacktest {
		now = c.backtestClock.Now
		// TODO(ejbrever) Implement the cancel order fake.
		return
	}
	for _, o := range c.inProgressBuyOrders() {
		if now.Sub(o.BuyOrder.CreatedAt) > 5*time.Minute {
			if err := c.alpacaClient.CancelOrder(o.BuyOrder.ID); err != nil {
				log.Printf("unable to cancel %q: %v", o.BuyOrder.ID, err)
			}
		}
	}
}

// sell initiates sell orders for all needed purchases.
func (c *client) sell() {
	boughtNotSelling := c.boughtNotSelling()
	if len(boughtNotSelling) == 0 {
		return
	}
	for _, p := range boughtNotSelling {
		c.placeSellOrder(p)
	}
}

func (c *client) placeSellOrder(p *purchase.Purchase) {
	// TODO(ejbrever) for debugging, remove this.
	log.Printf("BuyOrder before p.BuyFilledAvgPriceFloat: %+v", p.BuyOrder)
	basePrice := float64(p.BuyFilledAvgPriceFloat())
	if basePrice == 0 {
		log.Printf(
			"filledAvgPrice cannot be 0 for order:\nBuyOrder: %+v\n", p.BuyOrder)
		return
	}
	// Take a profit as soon as 0.2% profit can be achieved.
	profitLimitPrice := decimal.NewFromFloat(basePrice * 1.002)
	// Sell is 0.12% lower than base price (i.e. AvgFillPrice).
	stopPrice := decimal.NewFromFloat(basePrice - basePrice*.0012)
	// Set a limit on the sell price at 0.17% lower than the base price.
	lossLimitPrice := decimal.NewFromFloat(basePrice - basePrice*.0017)

	var err error
	req := &alpaca.PlaceOrderRequest{
		Side:        alpaca.Sell,
		AssetKey:    &c.stockSymbol,
		Type:        alpaca.Limit,
		Qty:         decimal.NewFromFloat(*purchaseQty),
		TimeInForce: alpaca.GTC,
		OrderClass:  alpaca.Oco,
		TakeProfit: &alpaca.TakeProfit{
			LimitPrice: &profitLimitPrice,
		},
		StopLoss: &alpaca.StopLoss{
			StopPrice:  &stopPrice,
			LimitPrice: &lossLimitPrice,
		},
	}
	if *runBacktest {
		c.fakePlaceSellOrder(p, req)
		return
	}
	sellOrder, err := c.alpacaClient.PlaceOrder(*req)
	if err != nil {
		log.Printf("unable to place sell order: %v\npurchase:\nbuy:%+v\nsell:%+v\n",
			err, p.BuyOrder, p.SellOrder)
		return
	}
	p.SellOrder = sellOrder
	log.Printf("sell order placed:\n%+v\n", p.SellOrder)

	if err := c.dbClient.Update(p); err != nil {
		log.Printf("unable to update for sell order:%v\n%+v", err, p)
	}
}

// Buy side: Look at most recent three 1 minute bars. If positive direction, buy.
func (c *client) buy(t time.Time) {
	if len(c.inProgressPurchases()) >= c.concurrentPurchases {
		log.Printf("allowable purchases used @ %v\n", t)
		return
	}
	if !c.buyEvent(t) {
		return
	}
	c.placeBuyOrder()
}

// buyEvent determines if this time is a buy event.
func (c *client) buyEvent(t time.Time) bool {
	limit := *numHistoricalBarsToUse
	endDt := time.Now()
	startDt := endDt.Add(time.Duration(-1**numHistoricalBarsToUse) * time.Minute)
	var bars []alpaca.Bar
	var err error
	switch {
	case *runBacktest:
		bars = c.fakeGetSymbolBars()
	default:
		bars, err = c.alpacaClient.GetSymbolBars(c.stockSymbol, alpaca.ListBarParams{
			Timeframe: "1Min",
			StartDt:   &startDt,
			EndDt:     &endDt,
			Limit:     &limit,
		})
	}
	if err != nil {
		log.Printf("GetSymbolBars err @ %v: %v\n", t, err)
		return false
	}
	if len(bars) < *numHistoricalBarsToUse {
		log.Printf(
			"did not return at least %v bars, so cannot proceed @ %v\ngot: %+v",
			*numHistoricalBarsToUse,
			t,
			bars,
		)
		return false
	}
	var a *alpaca.Account
	switch {
	case *runBacktest:
		a = c.fakeGetAccount()
	default:
		a, err = c.alpacaClient.GetAccount()
		if err != nil {
			log.Printf("unable to get account details to check for needed cash: %v", err)
			return false
		}
	}
	// neededCash is the amount of money needed to perform a purchase, with an
	// extra 20% buffer.
	neededCash := bars[0].Close * float32(*purchaseQty) * 1.2
	if a.Cash.LessThan(decimal.NewFromFloat32(neededCash)) {
		log.Printf("not enough cash to perform a trade, have %%%v, need %%%v", a.Cash, neededCash)
		return false
	}

	if !c.barsImprovementSlope(bars) {
		log.Printf("slope did not meet requirements")
		return false
	}

	if *allSequentialIncreasesToBuy && !c.allPositiveImprovements(bars) {
		log.Printf("non-positive improvements")
		return false
	}
	return true
}

// allPositiveImprovements returns true if each bar improves over the last.
func (c *client) allPositiveImprovements(bars []alpaca.Bar) bool {
	for i, b := range bars {
		if i == 0 {
			continue
		}
		if b.Close <= bars[i-1].Close {
			return false
		}
	}
	return true
}

// barsImprovementSlope returns true if the slope of the bars, using least
// squares regression, is greater than a specified value.
func (c *client) barsImprovementSlope(bars []alpaca.Bar) bool {
	if bars[len(bars)-1].Close < bars[0].Close {
		// Do a quick check to avoid more expensive math.
		return false
	}

	var sumX, sumY, sumX2, sumXY float64
	for xInt, bar := range bars {
		x := float64(xInt)
		y := float64(bar.Close)
		sumX += x
		sumY += y
		sumX2 += x * x
		sumXY += x * y
	}
	n := float64(len(bars))
	m := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)
	log.Printf("slope: %.2f", m)
	return m >= *minSlopeRequiredToBuy
}

func (c *client) placeBuyOrder() {
	req := &alpaca.PlaceOrderRequest{
		AccountID:   "",
		AssetKey:    &c.stockSymbol,
		Qty:         decimal.NewFromFloat(*purchaseQty),
		Side:        alpaca.Buy,
		Type:        alpaca.Market,
		TimeInForce: alpaca.Day,
	}
	var err error
	var o *alpaca.Order
	switch {
	case *runBacktest:
		c.fakePlaceBuyOrder(req)
		return
	default:
		o, err = c.alpacaClient.PlaceOrder(*req)
		if err != nil {
			log.Printf("unable to place buy order: %v", err)
			return
		}
	}
	p := &purchase.Purchase{
		BuyOrder: o,
	}
	c.purchases = append(c.purchases, p)
	log.Printf("buy order placed:\n%+v", o)

	if err := c.dbClient.Insert(p); err != nil {
		log.Printf("unable to insert buy order in database: %v", err)
	}
}

// closeOutTrading closes out all trading for the day.
func (c *client) closeOutTrading() {
	if *runBacktest {
		c.fakeCloseOutTrading()
		return
	}
	if err := c.alpacaClient.CancelAllOrders(); err != nil {
		log.Printf("unable to cancel all orders: %v\n", err)
	}
	if err := c.alpacaClient.CloseAllPositions(); err != nil {
		log.Printf("unable to close all positions: %v\n", err)
	}
	log.Printf("My trading is over for a bit and all trading is closed out!")
}

// order returns details for a given order. If the order was replaced, it
// returns details for the new order.
func (c *client) order(id string) *alpaca.Order {
	if *runBacktest {
		return c.fakeOrder(id)
	}
	order, err := c.alpacaClient.GetOrder(id)
	if err != nil {
		log.Printf("GetOrder %q error: %v", id, err)
		return nil
	}
	if order == nil {
		return nil
	}
	if order.ReplacedBy != nil {
		replacedOrder, err := c.alpacaClient.GetOrder(*order.ReplacedBy)
		if err != nil {
			log.Printf("Replaced GetOrder %q (original ID: %q) error: %v", *order.ReplacedBy, id, err)
			return nil
		}
		if replacedOrder == nil {
			return nil
		}
		order = replacedOrder
	}
	return order
}

// updateOrders updates all in progress orders with their latest details.
func (c *client) updateOrders() {
	for _, o := range c.inProgressBuyOrders() {
		order := c.order(o.BuyOrder.ID)
		if order == nil {
			continue
		}
		o.BuyOrder = order
		if err := c.dbClient.Update(o); err != nil {
			log.Printf("unable to update buy order:%v\n%+v", err, o)
		}
	}
	for _, o := range c.inProgressSellOrders() {
		order := c.order(o.SellOrder.ID)
		if order == nil {
			continue
		}
		o.SellOrder = order
		if err := c.dbClient.Update(o); err != nil {
			log.Printf("unable to update sell order:%v\n%+v", err, o)
		}
	}
}

// startWebserver starts a web server to display job information.
func startWebserver() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveHTTP)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
		log.Printf("defaulting to port %s", port)
	}

	log.Printf("listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func serveHTTP(w http.ResponseWriter, r *http.Request) {
	if trading {
		fmt.Fprintf(w, "Trader One is running and trading!\n\n")
	} else {
		fmt.Fprintf(w, "Trader One is running, but not currently trading.\n\n")
	}
}

func setupLogging() *os.File {
	filename := "trader-one-logs"
	if *runBacktest {
		filename += "-backtest"
	}
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	log.SetOutput(f)
	return f
}

func closeLogging(f *os.File) {
	log.Printf("shutting down")
	f.Close()
}

func main() {
	go startWebserver()

	f := setupLogging()
	defer closeLogging(f)

	if *runBacktest {
		backtest()
		return
	}

	c, err := new(*stockSymbol, *maxConcurrentPurchases)
	if err != nil {
		log.Printf("unable to start trader-one: %v", err)
		return
	}
	log.Printf("trader one is now online!")

	ticker := time.NewTicker(*durationBetweenAction)
	defer ticker.Stop()
	done := make(chan bool)
	go func() {
		time.Sleep(*durationToRun)
		done <- true
	}()
	for {
		select {
		case <-done:
			c.closeOutTrading()
			return
		case t := <-ticker.C:
			clock, err := c.alpacaClient.GetClock()
			if err != nil {
				log.Printf("error checking if market is open: %v", err)
				continue
			}
			c.updateOrders()
			switch {
			case clock.NextClose.Sub(time.Now()) < *timeBeforeMarketCloseToSell:
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
}

func init() {
	flag.Parse()

	os.Setenv("TZ", "America/Los_Angeles")
	os.Setenv(common.EnvApiKeyID, *apiKeyID)
	os.Setenv(common.EnvApiSecretKey, *apiSecretKey)

	log.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

	alpaca.SetBaseUrl(*apiEndpoint)

	var err error
	PST, err = time.LoadLocation("America/Los_Angeles")
	if err != nil {
		fmt.Printf("unable to load PST timezone location: %v", err)
		os.Exit(1)
	}

	EST, err = time.LoadLocation("America/New_York")
	if err != nil {
		fmt.Printf("unable to load EST timezone location: %v", err)
		os.Exit(1)
	}
}
