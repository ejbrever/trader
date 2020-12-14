// Package history retrieves historical data.
package main

import (
    "os"
    "fmt"
    "strings"
    "time"

    "github.com/alpacahq/alpaca-trade-api-go/alpaca"
    "github.com/alpacahq/alpaca-trade-api-go/common"
)

var (
  stockSymbol = "SPY"
  timeFrame = "15Min"
  startTime = time.Date(2020, time.November, 13, 0, 0, 0, 0, time.UTC)
  endTime = time.Date(2020, time.December, 11, 0, 0, 0, 0, time.UTC)
)

func init() {
    os.Setenv(common.EnvApiKeyID, "PKMYQANTSQ1QRQW9FSO6")
    os.Setenv(common.EnvApiSecretKey, "d5T9VG79siGgofz8snYZDX85wLnVQHtPDQfvRMET")

    fmt.Printf("Running w/ credentials [%v %v]\n", common.Credentials().ID, common.Credentials().Secret)

    alpaca.SetBaseUrl("https://paper-api.alpaca.markets")
}

func collectHistory(c *alpaca.Client) ([]alpaca.Bar, error) {
  startCollect := startTime
  var history []alpaca.Bar
  for {
    if startCollect.After(endTime) {
      break
    }
    // With a limit of 1000, this should handle around 30 days of 15Min Bars.
    // Will collect 20 days to be conservative.
    limit := 1000
    endCollect := startCollect.Add(24*20*time.Hour)
    fmt.Printf("startCollect: %v\n", startCollect)
    fmt.Printf("endCollect: %v\n", endCollect)
    fmt.Printf("\n\n")
    for {
      bars, err := c.GetSymbolBars(stockSymbol, alpaca.ListBarParams{
        Timeframe: timeFrame,
        StartDt:   &startCollect,
        EndDt:     &endCollect,
        Limit:     &limit,
      })
      if err != nil {
        if strings.Contains(err.Error(), "too many requests") {
          time.Sleep(time.Minute)
          continue
        }
        return nil, fmt.Errorf("GetSymbolBars err: %v", err)
      }
      history = append(history, bars...)
      startCollect = endCollect
      break
    }
  }
  return history, nil
}

func main() {

  c := alpaca.NewClient(common.Credentials())

  history, err := collectHistory(c)
  if err != nil {
    fmt.Printf("unable to collect history: %v\n", err)
  }

  for _, h := range history {
    fmt.Printf("%v: %v\n", h.Time, h.Close)
  }
}
