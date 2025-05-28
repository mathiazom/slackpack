package packing

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/rusq/slackdump/v3"
	"os"
)

func PackUsers(sd *slackdump.Session, dbClient *pgx.Conn) {
	users, err := sd.GetUsers(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "slackdumpclient 'GetUsers' failed: %v\n", err)
		return
	}

	for _, user := range users {
		jsonData, err := json.Marshal(user)
		if err != nil {
			fmt.Fprintf(os.Stderr, "JSON marshal failed: %v\n", err)
			continue
		}

		_, err = dbClient.Exec(context.Background(), "INSERT INTO \"user\" (public_id, data) VALUES ($1, $2)", user.ID, string(jsonData))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Insert failed for %s: %v\n", user.ID, err)
			continue
		}
	}

	fmt.Printf("Inserted %d users successfully\n", len(users))
}
