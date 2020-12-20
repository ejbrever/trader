package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"


	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/common"
	"github.com/ejbrever/trader/purchase"
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

// Purchase stores information related to a purchase.
type Purchase struct {
	BuyOrder  *alpaca.Order
	SellOrder *alpaca.Order
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
	if p.BuyOrder.FilledAt != nil {
		return false
	}
	if p.BuyOrder.ExpiredAt != nil {
		return false
	}
	if p.BuyOrder.CanceledAt != nil {
		return false
	}
	if p.BuyOrder.FailedAt != nil {
		return false
	}
	if p.BuyOrder.ReplacedAt != nil {
		return false
	}
	return true
}

// InProgressSellOrder determines if the sell order is still open and
// in progress.
func (p *Purchase) InProgressSellOrder() bool {
	if p.SellOrder == nil {
		return false
	}
	if p.SellOrder.FilledAt != nil {
		return false
	}
	if p.SellOrder.ExpiredAt != nil {
		return false
	}
	if p.SellOrder.CanceledAt != nil {
		return false
	}
	if p.SellOrder.FailedAt != nil {
		return false
	}
	if p.SellOrder.ReplacedAt != nil {
		return false
	}
	return true
}

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
	for _, p := range boughtNotSold {
		c.placeSellOrder(t, p)
	}
}

func (c *client) placeSellOrder(t time.Time, p *purchase.Purchase) {
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
	c.purchases = append(c.purchases, &purchase.Purchase{
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
	log.Printf("My hour of trading is over!")
}

type webserver struct {
	alpacaClient *alpaca.Client
}

// startServer starts a web server to display account data.
func startServer(alpacaClient *alpaca.Client) {
	w := &webserver{
		alpacaClient: alpacaClient,
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
	fmt.Fprintf(w, "Trader One Is Live!\n\n")

	a, err := ws.alpacaClient.GetAccount()
	if err != nil {
		fmt.Fprintf(w, "unable to get account info: %v", err)
		return
	}
	fmt.Fprintf(w, "Equity: $%v\n", a.Equity.String())
	fmt.Fprintf(w, "Cash: $%v\n", a.Cash.String())

	positions, err := ws.alpacaClient.ListPositions()
	if err != nil {
		fmt.Fprintf(w, "unable to get account positions: %v", err)
		return
	}
	fmt.Fprintf(w, "\n\nCurrent Positions\n")
	for _, p := range positions {
		fmt.Fprintf(w, "\nSymbol: %v\n", p.Symbol)
		fmt.Fprintf(w, "Qty: %v\n", p.Qty)
		fmt.Fprintf(w, "CurrentPrice: $%v\n", p.CurrentPrice.String())
		fmt.Fprintf(w, "Average entry price: $%v\n", p.EntryPrice.String())
		fmt.Fprintf(w, "Market value: $%v\n", p.MarketValue.String())
	}

	timePeriod := "14D"
	timeFrame := alpaca.Day1
	history, err := ws.alpacaClient.GetPortfolioHistory(
		&timePeriod, &timeFrame, nil, false)
	if err != nil {
		fmt.Fprintf(w, "unable to get daily account history: %v", err)
		return
	}
	// ProfitLoss    []decimal.Decimal `json:"profit_loss"`
	// ProfitLossPct []decimal.Decimal `json:"profit_loss_pct"`

	fmt.Fprintf(w, "\n\nHistory - 14 Days\n")
	for i, t := range history.Timestamp {
		fmt.Fprintf(w, "%v - $%v, Profit: $%v [%%%v]\n",
			time.Unix(t, 0),
			history.Equity[i],
			history.ProfitLoss[i],
			history.ProfitLossPct[i].Round(6),
		)
	}

	activities, err := ws.alpacaClient.GetAccountActivities(nil, nil)
	if err != nil {
		fmt.Fprintf(w, "unable to get account activities: %v", err)
		return
	}
	fmt.Fprintf(w, "\n\nRecent Activity\n")
	for _, a := range activities {
		fmt.Fprintf(w, "%v: [%v] %v, %v @ $%v\n",
			a.TransactionTime, a.Side, a.Symbol, a.Qty, a.Price)
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

	go startServer(c.alpacaClient)

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
				c.closeOutTrading()
				time.Sleep(timeBeforeMarketCloseToSell)
				continue
			case !clock.IsOpen:
				log.Printf("market is not open :(")
				continue
			default:
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
}
