// Package purchase stores and manages purchases.
package purchase

import (
  "time"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
)

var (
	// orderCompletedStates are states when an order receives no further updates.
	orderCompletedStates = map[string]bool{
		"filled": true,
		"cancelled": true,
		"expired": true,
		"stopped ": true,
		"rejected ": true,
		"suspended ": true,
	}

	// endedUnsuccessfullyStates are the states when an order was not filled and
	// will receive no further updates.
	endedUnsuccessfullyStates = map[string]bool{
		"cancelled": true,
		"expired": true,
		"stopped ": true,
		"rejected ": true,
		"suspended ": true,
	}

	// inProgressStates are states when an order is in-progress or filled.
	inProgressStates = map[string]bool{
		"new": true,
		"partially_filled": true,
		"done_for_day": true,
		"accepted ": true,
		"pending_new ": true,
		"accepted_for_bidding": true,
		"calculated": true,
	}
)

// Purchase stores information related to a purchase.
type Purchase struct {
  ID int64  // ID is a unique ID of Purchase and is stored in the database.
	BuyOrder  *alpaca.Order
	SellOrder *alpaca.Order
	SellFilledYearDay int  // The day of the year that the sale is made.
}

// SellFilled returns true when the sell order if filled.
func (p *Purchase) SellFilled() bool {
	if p.SellOrder == nil {
		return false
	}
	return p.SellOrder.Status == "filled"
}

// BuyFilled returns true when the full quantity is bought and order if filled.
func (p *Purchase) BuyFilled() bool {
	if p.BuyOrder == nil {
		return false
	}
	return p.BuyOrder.Status == "filled"
}

// SellHasStatus returns true when the sell order has the provided status.
func (p *Purchase) SellHasStatus(s string) bool {
	if p.SellOrder == nil {
		return false
	}
	return p.SellOrder.Status == s
}

// BuyHasStatus returns true when the buy order has the provided status.
func (p *Purchase) BuyHasStatus(s string) bool {
	if p.BuyOrder == nil {
		return false
	}
	return p.BuyOrder.Status == s
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

// BuyInProgress returns true when the buy order is at any in-progress stage.
func (p *Purchase) BuyInProgress() bool {
	if p.BuyOrder == nil {
		return false
	}
	return inProgressStates[p.BuyOrder.Status]
}

// SellInProgress returns true when the sell order is at any in-progress stage.
func (p *Purchase) SellInProgress() bool {
	if p.SellOrder == nil {
		return false
	}
	return inProgressStates[p.SellOrder.Status]
}

// GetSellFilledYearDay returns the year day in PST that the sell was filled.
func (p *Purchase) GetSellFilledYearDay(tz *time.Location) int {
	if p.SellFilledYearDay == 0 {
		p.SellFilledYearDay = p.SellOrder.FilledAt.In(tz).YearDay()
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

// NotSelling determines if the sell order is *not* in progress. This would be
// because an order has not been created or an order ended unsuccessfully.
func (p *Purchase) NotSelling() bool {
	if p.SellOrder == nil {
		return true
	}
	return endedUnsuccessfullyStates[p.SellOrder.Status]
}
