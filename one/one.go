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
)

var (
	// PST is the timezone for the Pacific time.
	PST *time.Location

	// Is trading currently allowed by the algorithm?
	trading bool
)

type client struct {
	concurrentPurchases int
	alpacaClient        *alpaca.Client
	dbClient            *database.Client
	purchases           []*purchase.Purchase
	stockSymbol         string
}

func new(stockSymbol string, concurrentPurchases int) (*client, error) {
	db, err := database.New()
	if err != nil {
		return nil, fmt.Errorf("unable to open db: %v", err)
	}
	// TODO(ejbrever) This needs to be all of TODAYS purchases only.
	purchases, err := db.Purchases()
	if err != nil {
		return nil, fmt.Errorf("unable to get all purchases: %v", err)
	}
	return &client{
		concurrentPurchases: concurrentPurchases,
		alpacaClient:        alpaca.NewClient(common.Credentials()),
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
	sellOrder, err := c.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
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
	})
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
	limit := 3
	endDt := time.Now()
	startDt := endDt.Add(-5 * time.Minute)
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
	if len(bars) < 3 {
		log.Printf(
			"did not return at least three bars, so cannot proceed @ %v\ngot: %+v",
			t, bars)
		return false
	}
	a, err := c.alpacaClient.GetAccount()
	if err != nil {
		log.Printf("unable to get account details to check for needed cash: %v", err)
		return false
	}
	// neededCash is the amount of money needed to perform a purchase, with an
	// extra 20% buffer.
	neededCash := bars[0].Close * float32(*purchaseQty) * 1.2
	if a.Cash.LessThan(decimal.NewFromFloat32(neededCash)) {
		log.Printf("not enough cash to perform a trade, have %%%v, need %%%v", a.Cash, neededCash)
		return false
	}

	if !c.allPositiveImprovements(bars) {
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

func (c *client) placeBuyOrder() {
	o, err := c.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		AccountID:   "",
		AssetKey:    &c.stockSymbol,
		Qty:         decimal.NewFromFloat(*purchaseQty),
		Side:        alpaca.Buy,
		Type:        alpaca.Market,
		TimeInForce: alpaca.Day,
	})
	if err != nil {
		log.Printf("unable to place buy order: %v", err)
		return
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
	f, err := os.OpenFile("trader-one-logs", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
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
	os.Setenv(common.EnvApiKeyID, *apiKeyID)
	os.Setenv(common.EnvApiSecretKey, *apiSecretKey)

	log.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

	alpaca.SetBaseUrl(*apiEndpoint)

	var err error
	PST, err = time.LoadLocation("America/Los_Angeles")
	if err != nil {
		fmt.Printf("unable to load timezone location: %v", err)
		os.Exit(1)
	}
}
