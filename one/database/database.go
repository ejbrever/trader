// Package database manages the databases for trader-one.
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/alpaca"
	"github.com/ejbrever/trader/one/purchase"

	// MySQL package.
	_ "github.com/go-sql-driver/mysql"
)

const (
	username = "one"
	password = "password"
	hostname = "127.0.0.1:3306"
	dbName   = "one"
)

// Client defines all funcs needed for the database client.
type Client interface {
	Insert(p *purchase.Purchase) error
	Purchases(yearDay int, tz *time.Location) ([]*purchase.Purchase, error)
	Update(p *purchase.Purchase) error
}

// MySQLClient manages interactions with the database.
type MySQLClient struct {
	db *sql.DB
}

// New creates a new database client that is connected to the database.
func New() (*MySQLClient, error) {
	db, err := open()
	if err != nil {
		return nil, err
	}
	return &MySQLClient{
		db: db,
	}, err
}

// Insert inserts purchase data into the table.
func (c *MySQLClient) Insert(p *purchase.Purchase) error {
	if p.ID != 0 {
		return fmt.Errorf("purchase cannot have a preexisting ID")
	}

	buyBytes, err := json.Marshal(p.BuyOrder)
	if err != nil {
		return fmt.Errorf("unable to marshal buy order: %v", err)
	}
	fmt.Printf("1")

	sellBytes, err := json.Marshal(p.SellOrder)
	if err != nil {
		return fmt.Errorf("unable to marshal sell order: %v", err)
	}
	fmt.Printf("2")

	query := `INSERT INTO trader_one(buy_order, sell_order) VALUES (?, ?)`
	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	fmt.Printf("3")
	defer cancelFunc()
	stmt, err := c.db.PrepareContext(ctx, query)
	fmt.Printf("4")
	if err != nil {
		return fmt.Errorf("unable to prepare SQL statement: %v", err)
	}
	defer stmt.Close()
	fmt.Printf("5")

	res, err := stmt.ExecContext(ctx, string(buyBytes), string(sellBytes))
	fmt.Printf("6")
	if err != nil {
		return fmt.Errorf("unable to insert row: %v", err)
	}
	id, err := res.LastInsertId()
	fmt.Printf("7")
	if err != nil {
		return fmt.Errorf("unable to find new ID: %v", err)
	}
	fmt.Printf("8")
	p.ID = id
	fmt.Printf("9")
	return nil
}

// Update updates purchase data into the table.
func (c *MySQLClient) Update(p *purchase.Purchase) error {
	if p.ID == 0 {
		return fmt.Errorf("purchase must have a preexisting ID")
	}

	buyBytes, err := json.Marshal(p.BuyOrder)
	if err != nil {
		return fmt.Errorf("unable to marshal buy order: %v", err)
	}

	sellBytes, err := json.Marshal(p.SellOrder)
	if err != nil {
		return fmt.Errorf("unable to marshal sell order: %v", err)
	}

	query := `UPDATE trader_one
  SET
    buy_order = ?,
    sell_order = ?,
    updated_at = NOW()
  WHERE
    id = ?`
	ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFunc()
	stmt, err := c.db.PrepareContext(ctx, query)
	if err != nil {
		return fmt.Errorf("unable to prepare SQL statement: %v", err)
	}
	defer stmt.Close()

	_, err = stmt.ExecContext(ctx, string(buyBytes), string(sellBytes), p.ID)
	if err != nil {
		return fmt.Errorf("unable to update row: %v", err)
	}
	return nil
}

// Purchases retrieves all purchases stored in the database for a given year day.
// The server is in UTC, however the timezone will be specified so PST can be used.
func (c *MySQLClient) Purchases(yearDay int, tz *time.Location) ([]*purchase.Purchase, error) {
	results, err := c.db.Query(`SELECT id, created_at, buy_order, sell_order FROM trader_one`)
	if err != nil {
		return nil, fmt.Errorf("unable to get purchases from table: %v", err)
	}

	var purchases []*purchase.Purchase
	for results.Next() {
		var id int64
		var buyOrderJSON, sellOrderJSON string
		var createdAt time.Time
		err = results.Scan(&id, &createdAt, &buyOrderJSON, &sellOrderJSON)
		if err != nil {
			return nil, fmt.Errorf("unable to scan row: %v", err)
		}
		if yearDay != createdAt.In(tz).YearDay() {
			continue
		}
		sellOrder := &alpaca.Order{}
		buyOrder := &alpaca.Order{}
		if err = json.Unmarshal([]byte(buyOrderJSON), buyOrder); err != nil {
			return nil, fmt.Errorf("unable to unmarshal %q: %v", buyOrderJSON, err)
		}
		if err = json.Unmarshal([]byte(sellOrderJSON), sellOrder); err != nil {
			return nil, fmt.Errorf("unable to unmarshal %q: %v", sellOrderJSON, err)
		}
		purchases = append(purchases, &purchase.Purchase{
			ID:        id,
			BuyOrder:  buyOrder,
			SellOrder: sellOrder,
		})
	}
	return purchases, nil
}

// open opens the database.
func open() (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn(dbName))
	if err != nil {
		return nil, fmt.Errorf("unable to open database %q: %v", dbName, err)
	}
	return db, nil
}

func dsn(dbName string) string {
	return fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true", username, password, hostname, dbName)
}
