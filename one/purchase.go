package main

import (
	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
)

// Purchase stores information related to a purchase.
type Purchase struct {
	BuyOrder  *alpaca.Order
	SellOrder *alpaca.Order
}

// BuyFilledAvgPriceFloat returns the average fill price of a buy event.
func (p *Purchase) BuyFilledAvgPriceFloat() float32 {
	f, _ := p.BuyOrder.FilledAvgPrice.Float64()
	return float32(f)
}

// SoldFilledAvgPriceFloat returns the average fill price of a sell event.
func (p *Purchase) SoldFilledAvgPriceFloat() float32 {
	f, _ := p.SellOrder.FilledAvgPrice.Float64()
	return float32(f)
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
