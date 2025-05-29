package main

import (
	"context"
	"flag"
	"log"
	"os"

	. "github.com/mathiazom/slackpack/internal/databaseclient"
	. "github.com/mathiazom/slackpack/internal/migrate"
	. "github.com/mathiazom/slackpack/internal/packing"
	. "github.com/mathiazom/slackpack/internal/slackdumpclient"
)

func main() {
	var migrate = flag.Bool("migrate", false, "run database migrations")
	flag.Parse()

	if *migrate {
		if err := RunMigrations(); err != nil {
			log.Fatal(err)
		}
		return
	}

	sd := CreateSlackdumpClient()
	if sd == nil {
		os.Exit(1)
	}

	db := CreateDatabaseClient()
	if db == nil {
		os.Exit(1)
	}

	defer db.Close(context.Background())

	channels, _ := PackChannels(sd, db)
	PackUsers(sd, db)
	PackMessagesFromChannels(channels, sd, db)
}
