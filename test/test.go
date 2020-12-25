package main

import (
	"fmt"
	"log"
	"os"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/alpacahq/alpaca-trade-api-go/common"
	"github.com/ejbrever/trader/one/database"
	"github.com/ejbrever/trader/one/purchase"
)

func main() {
	c := alpaca.NewClient(common.Credentials())
	id := "4f88247a-a53b-4463-809d-3940666db0d9"
	o, err := c.GetOrder(id)
	if err != nil {
		fmt.Printf("GetOrder %q error: %v\n", id, err)
		return
	}
	fmt.Printf("full: %+v\n\n", o)
	if o.ReplacedBy != nil {
		fmt.Printf("replacedby: %v\n", o.ReplacedBy)
	}
	if o.Legs != nil {
		for _, l := range *o.Legs {
			fmt.Printf("leg: %+v\n", l)
		}
	}

	db, err := database.New()
	if err != nil {
		fmt.Printf("unable to open db: %v", err)
		return
	}
	if err = db.Insert(&purchase.Purchase{
		BuyOrder:  o,
		SellOrder: o,
	}); err != nil {
		fmt.Printf("unable to insert row: %v", err)
	}
}

func init() {
	os.Setenv(common.EnvApiKeyID, "PKMYQANTSQ1QRQW9FSO6")
	os.Setenv(common.EnvApiSecretKey, "d5T9VG79siGgofz8snYZDX85wLnVQHtPDQfvRMET")

	log.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

	alpaca.SetBaseUrl("https://paper-api.alpaca.markets")
}
