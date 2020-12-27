package main

import (
	"log"
	"time"
)

func backtest() {
	c, err := new(*stockSymbol, *maxConcurrentPurchases)
	if err != nil {
		log.Printf("unable to start backtesting trader-one: %v", err)
		return
	}
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

type fakeClock struct {
	NextClose time.Time
	IsOpen    bool
}

func getFakeClock() *fakeClock {
	return &fakeClock{}
}
