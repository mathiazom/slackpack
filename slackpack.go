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

	sdClient := CreateSlackdumpClient()
	if sdClient == nil {
		os.Exit(1)
	}

	dbClient := CreateDatabaseClient()
	if dbClient == nil {
		os.Exit(1)
	}

	defer dbClient.Close(context.Background())

	PackChannels(sdClient, dbClient)
	PackUsers(sdClient, dbClient)
}
