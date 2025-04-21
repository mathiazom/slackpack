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
	var packUserFlag = flag.Bool("user", false, "run user packing")
	var packEmojiFlag = flag.Bool("emoji", false, "run emoji packing")
	var packChannelFlag = flag.Bool("channel", false, "run channel packing")
	var packMessageFlag = flag.Bool("message", false, "run message packing")
	var migrate = flag.Bool("migrate", false, "run database migrations")
	flag.Parse()

	packAll := !*packUserFlag && !*packEmojiFlag && !*packChannelFlag && !*packMessageFlag

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

	seaweedMasterUrl := os.Getenv("SEAWEEDFS_MASTER_URL")

	if packAll || *packChannelFlag || *packMessageFlag {
		channels, err := PackChannels(sd, db)
		if err == nil && (packAll || *packMessageFlag) {
			PackMessagesFromChannels(channels, sd, db)
		}
	}

	if packAll || *packUserFlag {
		PackUsers(sd, db)
	}
	if packAll || *packEmojiFlag {
		PackEmojis(sd, db, seaweedMasterUrl)
	}
}
