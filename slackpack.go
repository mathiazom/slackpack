package main

import (
	"context"
	"os"

	. "github.com/mathiazom/slackpack/internal/databaseclient"
	. "github.com/mathiazom/slackpack/internal/packing"
	. "github.com/mathiazom/slackpack/internal/slackdumpclient"
)

func main() {
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
