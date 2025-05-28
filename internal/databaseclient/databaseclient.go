package databaseclient

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
)

func CreateDatabaseClient() *pgx.Conn {
	db, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_CONNECTION_STRING"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to databaseclient: %v\n", err)
		return nil
	}
	return db
}
