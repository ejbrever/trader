// Creates a new database.
// After creating the database, this mysql command can be used to create the
// table for trader-one:
// CREATE TABLE IF NOT EXISTS trader-one(
//   id int primary key auto_increment,
//   buy_order JSON,
//   sell_order JSON,
//   created_at datetime default CURRENT_TIMESTAMP,
//   updated_at datetime default CURRENT_TIMESTAMP)
package main

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "time"

    _ "github.com/go-sql-driver/mysql"
)

const (
    username = "one"
    password = "password"
    hostname = "127.0.0.1:3306"
    dbName   = "one"
    createDatabaseCmd = "CREATE DATABASE IF NOT EXISTS %s"
)

func dsn(dbName string) string {
    return fmt.Sprintf("%s:%s@tcp(%s)/%s", username, password, hostname, dbName)
}

func main() {
    db, err := sql.Open("mysql", dsn(""))
    if err != nil {
        log.Printf("Error %s when opening DB\n", err)
        return
    }
    defer db.Close()

    ctx, cancelFunc := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelFunc()
    res, err := db.ExecContext(ctx, fmt.Sprintf(createDatabaseCmd, dbName))
    if err != nil {
        log.Printf("unable to create database: %v", err)
        return
    }

    db.Close()
    db, err = sql.Open("mysql", dsn(dbName))
    if err != nil {
        log.Printf("unable to open database %q: %v", dbName, err)
        return
    }
    defer db.Close()

    query := `CREATE TABLE IF NOT EXISTS trader-one(
      id int primary key auto_increment,
      buy_order json,
      sell_order json,
      created_at datetime default CURRENT_TIMESTAMP,
      updated_at datetime default CURRENT_TIMESTAMP
    )`
    ctx, cancelFunc = context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelfunc()
    res, err := db.ExecContext(ctx, query)
    if err != nil {
      log.Printf("unable to create table: %v", err)
      return err
    }

    db.SetMaxOpenConns(3)
    db.SetMaxIdleConns(5)
    db.SetConnMaxLifetime(time.Minute * 5)

    ctx, cancelFunc = context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelFunc()
    err = db.PingContext(ctx)
    if err != nil {
        log.Printf("unable to ping database: %v", err)
        return
    }
    log.Printf("Connected to database %q successfully\n", dbName)
}
