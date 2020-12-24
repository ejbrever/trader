// Creates a new database.
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
    username = "root"
    password = ""
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

    ctx, cancelfunc := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelfunc()
    res, err := db.ExecContext(ctx, fmt.Sprintf(createDatabaseCmd, dbName))
    if err != nil {
        log.Printf("unable to create database: %v", err)
        return
    }
    rowsAffected, err := res.RowsAffected()
    if err != nil {
        log.Printf("unable to fetch rows: %v", err)
        return
    }
    log.Printf("rows affected: %d\n", rowsAffected)

    db.Close()
    db, err = sql.Open("mysql", dsn(dbName))
    if err != nil {
        log.Printf("unable to open database %q: %v", dbName, err)
        return
    }
    defer db.Close()

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
