package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"


	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/common"
	"github.com/shopspring/decimal"
)

const (
	// purchaseQty is the quantity of shares to purchase with each buy order.
	purchaseQty = 10

	// timeToTrade is the time that the service should continue trying to trade.
	timeToTrade = 1000 * time.Hour

	// timeBetweenAction is the time between each attempt to buy or sell.
	timeBetweenAction = 30 * time.Second

	// stockSymbol is the stock to buy an sell.
	stockSymbol = "SPY"

	// maxAllowedPurchases is the maximum number of allowed purchases.
	maxAllowedPurchases = 20

	// timeBeforeMarketCloseToSell is the duration of time before market close
	// that all positions should be closed out.
	timeBeforeMarketCloseToSell = 1*time.Hour
)

var (
	// PST is the timezone for the Pacific time.
	PST *time.Location

	// orderCompletedStates are states when an order receives no further updates.
	orderCompletedStates = map[string]bool{
		"filled": true,
		"cancelled": true,
		"expired": true,
		"replaced": true,
		"stopped ": true,
		"rejected ": true,
		"suspended ": true,
	}

	// inProgressOrFilledStates are states when an order is in-progress or filled.
	inProgressOrFilledStates = map[string]bool{
		"new": true,
		"partially_filled": true,
		"filled": true,
		"done_for_day": true,
		"accepted ": true,
		"pending_new ": true,
		"accepted_for_bidding": true,
		"calculated": true,
	}
)

// Purchase stores information related to a purchase.
type Purchase struct {
	BuyOrder  *alpaca.Order
	SellOrder *alpaca.Order
	SellFilledYearDay int  // The day of the year that the sale is made.
}

// AllSold returns true when the full quantity is sold and order if filled.
func (p *Purchase) AllSold() bool {
	if p.SellOrder == nil {
		return false
	}
	return p.SellOrder.Status == "filled"
}

// AllBought returns true when the full quantity is bought and order if filled.
func (p *Purchase) AllBought() bool {
	if p.BuyOrder == nil {
		return false
	}
	return p.BuyOrder.Status == "filled"
}

// BuyInitiatedAndNotFilled returns true when the buy order is created and not
// yet filled.
func (p *Purchase) BuyInitiatedAndNotFilled() bool {
	if p.BuyOrder == nil {
		return false
	}
	if p.BuyOrder.CreatedAt.IsZero() {
		return false
	}
	if p.BuyOrder.Status == "filled" {
		return false
	}
	return true
}

// BuyInProgressOrFilled returns true when the buy order is at any non-cancelled
// stage.
func (p *Purchase) BuyInProgressOrFilled() bool {
	if p.BuyOrder == nil {
		return false
	}
	return inProgressOrFilledStates[p.BuyOrder.Status]
}


// GetSellFilledYearDay returns the year day in PST that the sell was filled.
func (p *Purchase) GetSellFilledYearDay() int {
	if p.SellFilledYearDay == 0 {
		p.SellFilledYearDay = p.SellOrder.FilledAt.In(PST).YearDay()
	}
	return p.SellFilledYearDay
}

// BuyFilledAvgPriceFloat returns the average fill price of a buy event.
func (p *Purchase) BuyFilledAvgPriceFloat() float64 {
	f, _ := p.BuyOrder.FilledAvgPrice.Float64()
	return f
}

// SoldFilledAvgPriceFloat returns the average fill price of a sell event.
func (p *Purchase) SoldFilledAvgPriceFloat() float64 {
	f, _ := p.SellOrder.FilledAvgPrice.Float64()
	return f
}

// InProgressBuyOrder determines if the buy order is still open and in progress.
func (p *Purchase) InProgressBuyOrder() bool {
	if p.BuyOrder == nil {
		return false
	}
	return !orderCompletedStates[p.BuyOrder.Status]
}

// InProgressSellOrder determines if the sell order is still open and
// in progress.
func (p *Purchase) InProgressSellOrder() bool {
	if p.SellOrder == nil {
		return false
	}
	return !orderCompletedStates[p.SellOrder.Status]
}

// NotSelling determines if the sell order is *not* in progress.
func (p *Purchase) NotSelling() bool {
	if p.SellOrder == nil {
		return true
	}
	return orderCompletedStates[p.SellOrder.Status]

}

type client struct {
	allowedPurchases int
	alpacaClient     *alpaca.Client
	purchases        []*Purchase
	stockSymbol      string
	trading          bool  // Is trading currently allowed by the algo?
}

func new(stockSymbol string, allowedPurchases int) *client {
	return &client{
		allowedPurchases: allowedPurchases,
		alpacaClient:     alpaca.NewClient(common.Credentials()),
		stockSymbol:      stockSymbol,
	}
}

// boughtNotSelling returns a slice of purchases that have been bought and
// and a sell order is not placed.
func (c *client) boughtNotSelling() []*Purchase {
	var notSelling []*Purchase
	for _, p := range c.purchases {
		if !p.AllBought() {
			continue
		}
		if p.NotSelling() {
			notSelling = append(notSelling, p)
		}
	}
	return notSelling
}

// buyOrderAtAnyValidStageButNotSold returns a slice of purchases where the buy
// is at any valid stage (in progress or filled) and has not been entirely sold.
func (c *client) buyOrderAtAnyValidStageButNotSold() []*Purchase {
	var notSold []*Purchase
	for _, p := range c.purchases {
		if !p.BuyInProgressOrFilled() {
			continue
		}
		if !p.AllSold() {
			notSold = append(notSold, p)
		}
	}
	return notSold
}

// inProgressBuyOrders returns a slice of all buy purchases which are still
// open and in progress.
func (c *client) inProgressBuyOrders() []*Purchase {
	var inProgress []*Purchase
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
func (c *client) inProgressSellOrders() []*Purchase {
	var inProgress []*Purchase
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
	c.cancelOutdatedOrders()
	c.buy(t)
	c.sell(t)
}

// cancelOutdatedOrders cancels all buy orders that have been outstanding for
// more than 5 mins.
func (c *client) cancelOutdatedOrders() {
	now := time.Now()
	for _, o := range c.inProgressBuyOrders() {
		if now.Sub(o.BuyOrder.CreatedAt) > 5 * time.Minute {
			if err := c.alpacaClient.CancelOrder(o.BuyOrder.ID); err != nil {
				log.Printf("unable to cancel %q: %v", o.BuyOrder.ID, err)
			}
		}
	}
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
		if o.SellOrder.ReplacedBy != nil {
			o.SellOrder, err = c.alpacaClient.GetOrder(*o.SellOrder.ReplacedBy)
			if err != nil {
				return fmt.Errorf("GetOrder %q error: %v", id, err)
			}
		}
	}
	return nil
}

// Sell side:
// If current price greater than buy price, then sell.
func (c *client) sell(t time.Time) {
	boughtNotSelling := c.boughtNotSelling()
	if len(boughtNotSelling) == 0 {
		return
	}
	for _, p := range boughtNotSelling {
		c.placeSellOrder(t, p)
	}
}

func (c *client) placeSellOrder(t time.Time, p *Purchase) {
	basePrice := float64(p.BuyFilledAvgPriceFloat())
	if basePrice == 0 {
		log.Printf("filledAvgPrice cannot be 0 for order @ %v:\n%+v", t, p.BuyOrder)
		return
	}
	// Take a profit as soon as 0.2% profit can be achieved.
	profitLimitPrice := decimal.NewFromFloat(basePrice * 1.002)
	// Sell is 0.12% lower than base price (i.e. AvgFillPrice).
	stopPrice := decimal.NewFromFloat(basePrice - basePrice * .0012)
	// Set a limit on the sell price at 0.17% lower than the base price.
	lossLimitPrice := decimal.NewFromFloat(basePrice - basePrice * .0017)

	var err error
	p.SellOrder, err = c.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		Side:        alpaca.Sell,
		AssetKey:    &c.stockSymbol,
		Type:        alpaca.Limit,
		Qty:         decimal.NewFromFloat(purchaseQty),
		TimeInForce: alpaca.GTC,
		OrderClass: alpaca.Oco,
		TakeProfit: &alpaca.TakeProfit{
			LimitPrice: &profitLimitPrice,
		},
		StopLoss: &alpaca.StopLoss{
			StopPrice: &stopPrice,
			LimitPrice: &lossLimitPrice,
		},
	})
	if err != nil {
		log.Printf("unable to place sell order @ %v: %v", t, err)
		return
	}
	log.Printf("sell order placed @ %v:\n%+v", t, p.SellOrder)
}

// Buy side:
// Look at most recent two 1sec Bars.
// If positive direction, buy.
func (c *client) buy(t time.Time) {
	if len(c.buyOrderAtAnyValidStageButNotSold()) >= c.allowedPurchases {
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
	limit := 3
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
	if len(bars) < 3 {
		log.Printf("did not return at least three bars, so cannot proceed @ %v\n", t)
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
	for i, b := range bars{
		if i == 0 {
			continue
		}
		if b.Close <= bars[i-1].Close {
			return false
		}
	}
	return true
}

func (c *client) placeBuyOrder(t time.Time) {
	o, err := c.alpacaClient.PlaceOrder(alpaca.PlaceOrderRequest{
		AccountID:     "",
		AssetKey:      &c.stockSymbol,
		Qty:           decimal.NewFromFloat(purchaseQty),
		Side:          alpaca.Buy,
		Type:          alpaca.Market,
		TimeInForce:   alpaca.Day,
		ClientOrderID: fmt.Sprintf("one:%v", t),
	})
	if err != nil {
		log.Printf("unable to place buy order @ %v: %v\n", t, err)
		return
	}
	c.purchases = append(c.purchases, &Purchase{
		BuyOrder: o,
	})
	log.Printf("buy order placed @ %v:\n%+v\n", t, o)
}

// closeOutTrading closes out all trading for the day.
func (c *client) closeOutTrading() {
	if err := c.alpacaClient.CancelAllOrders(); err != nil {
		log.Printf("unable to cancel all orders: %v\n", err)
	}
	if err := c.alpacaClient.CloseAllPositions(); err != nil {
		log.Printf("unable to close all positions: %v\n", err)
	}
	log.Printf("My trading is over for a bit!")
}

// todaysCompletedPurchases returns all purchases in which the sell was
// completed today in PST.
func (c *client) todaysCompletedPurchases() []*Purchase {
	var today []*Purchase
	todayYearDay := time.Now().In(PST).YearDay()
	for _, p := range c.purchases {
		if !p.AllSold() {
			continue
		}
		if p.GetSellFilledYearDay() != todayYearDay {
			continue
		}
		today = append(today, p)
	}
	return today
}

type webserver struct {
	client *client
}

// startServer starts a web server to display account data.
func startServer(client *client) {
	w := &webserver{
		client: client,
	}
	mux := http.NewServeMux()
	mux.Handle("/", w)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("defaulting to port %s", port)
	}

	log.Printf("listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

func (ws *webserver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if ws.client.trading {
		fmt.Fprintf(w, "Trader One is running and trading!\n\n")
	} else {
		fmt.Fprintf(w, "Trader One is running, but not currently trading.\n\n")
	}


	a, err := ws.client.alpacaClient.GetAccount()
	if err != nil {
		fmt.Fprintf(w, "unable to get account info: %v", err)
		return
	}
	fmt.Fprintf(w, "Equity: $%v\n", a.Equity.StringFixed(2))
	fmt.Fprintf(w, "Cash: $%v\n", a.Cash.StringFixed(2))
	fmt.Fprintf(w, "Purchases open: %v/%v\n",
		len(ws.client.buyOrderAtAnyValidStageButNotSold()),
		ws.client.allowedPurchases)

	positions, err := ws.client.alpacaClient.ListPositions()
	if err != nil {
		fmt.Fprintf(w, "unable to get account positions: %v", err)
		return
	}
	fmt.Fprintf(w, "\n\nCurrent Positions\n")
	for _, p := range positions {
		fmt.Fprintf(w, "\nSymbol: %v\n", p.Symbol)
		fmt.Fprintf(w, "Qty: %v\n", p.Qty)
		fmt.Fprintf(w, "CurrentPrice: $%v\n", p.CurrentPrice.StringFixed(2))
		fmt.Fprintf(w, "Average entry price: $%v\n", p.EntryPrice.StringFixed(2))
		fmt.Fprintf(w, "Market value: $%v\n", p.MarketValue.StringFixed(2))
	}

	timePeriod := "14D"
	timeFrame := alpaca.Day1
	history, err := ws.client.alpacaClient.GetPortfolioHistory(
		&timePeriod, &timeFrame, nil, false)
	if err != nil {
		fmt.Fprintf(w, "unable to get daily account history: %v", err)
		return
	}
	fmt.Fprintf(w, "\n\nHistory - 14 Days\n")
	for i, t := range history.Timestamp {
		fmt.Fprintf(w, "%v: $%v, Profit: $%v [%%%v]\n",
			time.Unix(t, 0),
			history.Equity[i],
			history.ProfitLoss[i],
			history.ProfitLossPct[i].Mul(decimal.NewFromInt(100)).Round(3),
		)
	}

	fmt.Fprintf(w, "\n\nToday's Completed Wins/Losses\n")
	for _, p := range ws.client.todaysCompletedPurchases() {
		fmt.Fprintf(w, "Sold @ %v: %v, Qty: %v [$%v => $%v] \n",
			p.SellOrder.FilledAt.In(PST),
			p.SellOrder.Symbol,
			p.SellOrder.Qty,
			p.BuyOrder.FilledAvgPrice.StringFixed(2),
			p.SellOrder.FilledAvgPrice.StringFixed(2),
		)
	}

	activities, err := ws.client.alpacaClient.GetAccountActivities(nil, nil)
	if err != nil {
		fmt.Fprintf(w, "unable to get account activities: %v", err)
		return
	}
	fmt.Fprintf(w, "\n\nRecent Activity\n")
	for _, a := range activities {
		fmt.Fprintf(w, "%v: [%v] %v, %v @ $%v\n",
			a.TransactionTime.In(PST), a.Side, a.Symbol, a.Qty, a.Price)
	}

	fmt.Fprintf(w, "\n\nDeep dive of purchases\n")
	for _, p := range ws.client.purchases {
		fmt.Fprintf(w, "\nbuy order: %+v", p.BuyOrder)
		fmt.Fprintf(w, "sell order: %+v\n", p.SellOrder)
	}
}

func setupLogging() *os.File {
	f, err := os.OpenFile("trader-one-logs", os.O_RDWR | os.O_CREATE | os.O_APPEND, 0666)
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
	f := setupLogging()
	defer closeLogging(f)

	c := new(stockSymbol, maxAllowedPurchases)
	log.Printf("trader one is now online!")

	go startServer(c)

	ticker := time.NewTicker(timeBetweenAction)
	defer ticker.Stop()
	done := make(chan bool)
	go func() {
		time.Sleep(timeToTrade)
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
			switch {
			case clock.NextClose.Sub(time.Now()) < timeBeforeMarketCloseToSell:
				log.Printf("market is closing soon")
				c.trading = false
				c.closeOutTrading()
				time.Sleep(timeBeforeMarketCloseToSell)
				continue
			case !clock.IsOpen:
				c.trading = false
				log.Printf("market is not open :(")
				continue
			default:
				c.trading = true
				log.Printf("market is open!")
			}
			go c.run(t)
		}
	}
}

func init() {
	os.Setenv(common.EnvApiKeyID, "PKMYQANTSQ1QRQW9FSO6")
	os.Setenv(common.EnvApiSecretKey, "d5T9VG79siGgofz8snYZDX85wLnVQHtPDQfvRMET")

	log.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

	alpaca.SetBaseUrl("https://paper-api.alpaca.markets")

	var err error
	PST, err = time.LoadLocation("America/Los_Angeles")
	if err != nil {
		fmt.Printf("unable to load timezone location: %v", err)
		os.Exit(1)
	}
}
