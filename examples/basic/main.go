// Command basic demonstrates using the go-hsql driver against a running HSQLDB
// server. Start a server first, e.g.:
//
//	java -cp .hsqldb/hsqldb.jar org.hsqldb.server.Server \
//	  --database.0 mem:demo --dbname.0 demo --port 9001
//
// then run:
//
//	go run ./examples/basic "hsql://SA@localhost:9001/demo"
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/rennerdo30/go-hsql"
)

func main() {
	dsn := "hsql://SA@localhost:9001/demo"
	if len(os.Args) > 1 {
		dsn = os.Args[1]
	}

	db, err := sql.Open("hsql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping: %v", err)
	}
	fmt.Println("connected")

	mustExec(ctx, db, `CREATE TABLE IF NOT EXISTS book (
		id     INTEGER PRIMARY KEY,
		title  VARCHAR(200),
		price  DECIMAL(8,2),
		added  TIMESTAMP
	)`)
	mustExec(ctx, db, "DELETE FROM book")

	stmt, err := db.PrepareContext(ctx, "INSERT INTO book VALUES (?,?,?,?)")
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()
	for i, title := range []string{"The Go Programming Language", "SQL for Smarties"} {
		if _, err := stmt.ExecContext(ctx, i+1, title, fmt.Sprintf("%d.99", 30+i), time.Now()); err != nil {
			log.Fatal(err)
		}
	}

	rows, err := db.QueryContext(ctx, "SELECT id, title, price FROM book ORDER BY id")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id    int
			title string
			price string
		)
		if err := rows.Scan(&id, &title, &price); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  #%d  %-32s  $%s\n", id, title, price)
	}
	if err := rows.Err(); err != nil {
		log.Fatal(err)
	}
}

func mustExec(ctx context.Context, db *sql.DB, query string, args ...any) {
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		log.Fatalf("exec %q: %v", query, err)
	}
}
