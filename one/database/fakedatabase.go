package database

import "github.com/ejbrever/trader/one/purchase"

// FakeClient mocks interactions with the database.
type FakeClient struct{}

// NewFake returns a FakeClient for testing.
func NewFake() (*FakeClient, error) {
	return &FakeClient{}, nil
}

// Insert returns a fake Insert func for testing.
func (f *FakeClient) Insert(p *purchase.Purchase) error {
	return nil
}

// Purchases returns a fake Purchases func for testing.
func (f *FakeClient) Purchases() ([]*purchase.Purchase, error) {
	return nil, nil
}

// Update returns a fake Update func for testing.
func (f *FakeClient) Update(p *purchase.Purchase) error {
	return nil
}
