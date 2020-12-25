package main

import (
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
	// PST is the timezone for the Pacific time.
	PST *time.Location
)

// Webserver manages the webserver.
type Webserver struct {
	alpacaClient *alpaca.Client
	db           *database.Client
}

// New creates a new webserver.
func New() (*Webserver, error) {
	db, err := database.New()
	if err != nil {
		return nil, fmt.Errorf("unable to open db: %v", err)
	}
	return &Webserver{
		alpacaClient: alpaca.NewClient(common.Credentials()),
		db:           db,
	}, nil
}

// Start is a blocking call which starts the webserver.
func (ws *Webserver) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.main)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		fmt.Printf("defaulting to port %s\n", port)
	}

	fmt.Printf("listening on port %s\n", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}

// inProgressPurchases returns a slice of purchases where the buy is at any
// valid stage (in progress or filled) and has not been entirely sold.
func (ws *Webserver) inProgressPurchases(allPurchases []*purchase.Purchase) []*purchase.Purchase {
	var inProgress []*purchase.Purchase
	for _, p := range allPurchases {
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

// main serves information for the main page.
func (ws *Webserver) main(w http.ResponseWriter, r *http.Request) {
	allPurchases, err := ws.db.Purchases()
	if err != nil {
		fmt.Fprintf(w, "unable to get all purchases from database: %v", err)
		return
	}

	a, err := ws.alpacaClient.GetAccount()
	if err != nil {
		fmt.Fprintf(w, "unable to get account info: %v", err)
		return
	}
	fmt.Fprintf(w, "Equity: $%v\n", a.Equity.StringFixed(2))
	fmt.Fprintf(w, "Cash: $%v\n", a.Cash.StringFixed(2))
	fmt.Fprintf(w, "Purchases open: %v/20\n", len(ws.inProgressPurchases(allPurchases)))

	positions, err := ws.alpacaClient.ListPositions()
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
	history, err := ws.alpacaClient.GetPortfolioHistory(
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
	// This can be based on database now!
	// for _, p := range ws.todaysCompletedPurchases() {
	// 	fmt.Fprintf(w, "Sold @ %v: %v, Qty: %v [$%v => $%v] \n",
	// 		p.SellOrder.FilledAt.In(PST),
	// 		p.SellOrder.Symbol,
	// 		p.SellOrder.Qty,
	// 		p.BuyOrder.FilledAvgPrice.StringFixed(2),
	// 		p.SellOrder.FilledAvgPrice.StringFixed(2),
	// 	)
	// }

	activities, err := ws.alpacaClient.GetAccountActivities(nil, nil)
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

	// Switch to database.
	// for _, p := range ws.purchases {
	// 	fmt.Fprintf(w, "\nbuy order: %+v", p.BuyOrder)
	// 	fmt.Fprintf(w, "sell order: %+v\n", p.SellOrder)
	// }
}

func main() {
	w, err := New()
	if err != nil {
		fmt.Printf("unable to create webserver: %v", err)
		return
	}
	w.Start()
}

func init() {
	os.Setenv(common.EnvApiKeyID, "PKACWN8W5WFG2M5LNPEQ")
	os.Setenv(common.EnvApiSecretKey, "1VzEyqvSO60TLo3X2jlUEiNm8IQe8S3vWeOnZVs3")

	log.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

	alpaca.SetBaseUrl("https://paper-api.alpaca.markets")

	var err error
	PST, err = time.LoadLocation("America/Los_Angeles")
	if err != nil {
		fmt.Printf("unable to load timezone location: %v", err)
		os.Exit(1)
	}
}
