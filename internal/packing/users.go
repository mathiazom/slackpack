package packing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mathiazom/slackpack/internal/dbutils"
	"github.com/rusq/slackdump/v3"
	"os"
)

func PackUsers(sd *slackdump.Session, dbClient *pgx.Conn) {
	users, err := sd.GetUsers(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdumpclient 'GetUsers' failed: %v\n", err)
		return
	}

	count := 0
	for _, user := range users {
		jsonData, err := json.Marshal(user)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed: %v\n", err)
			continue
		}
		_, err = dbClient.Exec(context.Background(), "INSERT INTO \"user\" (public_id, data) VALUES ($1, $2)", user.ID, string(jsonData))
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == dbutils.ErrCodeUniqueConstraintViolation {
				// user is up-to-date
				continue
			}
			fmt.Fprintf(os.Stderr, "Insert failed for user %s: %v\n", user.ID, err)
			continue
		}

		fmt.Printf("Inserted new snapshot for user %s\n", user.ID)
		count++
	}

	if count == 0 {
		fmt.Printf("User snapshots are up-to-date\n")
		return
	}

	fmt.Printf("Inserted %d user snapshots\n", count)
}
