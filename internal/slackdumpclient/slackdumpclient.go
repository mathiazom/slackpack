package slackdumpclient

import (
	"context"
	"fmt"
	"github.com/rusq/slackdump/v3"
	"github.com/rusq/slackdump/v3/auth"
	"os"
)

func CreateSlackdumpClient() *slackdump.Session {
	slackToken := os.Getenv("SLACK_AUTH_TOKEN")
	slackCookie := os.Getenv("SLACK_AUTH_COOKIE")
	provider, err := auth.NewValueAuth(
		slackToken,
		slackCookie)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authenticating slackdump failed: %v\n", err)
		return nil
	}
	sd, err := slackdump.New(context.Background(), provider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Initializing slackdump failed: %v\n", err)
		return nil
	}
	return sd
}
