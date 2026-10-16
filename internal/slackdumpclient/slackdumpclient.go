package slackdumpclient

import (
	"context"
	"fmt"
	"github.com/rusq/slackdump/v3"
	"github.com/rusq/slackdump/v3/auth"
	"os"
)

// CreateSlackdumpClient also returns its auth provider, for raw API calls that
// slackdump's typed client would lose fields of.
func CreateSlackdumpClient() (*slackdump.Session, auth.Provider) {
	slackToken := os.Getenv("SLACK_AUTH_TOKEN")
	slackCookie := os.Getenv("SLACK_AUTH_COOKIE")
	provider, err := auth.NewValueAuth(
		slackToken,
		slackCookie)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authenticating slackdump failed: %v\n", err)
		return nil, nil
	}
	sd, err := slackdump.New(context.Background(), provider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Initializing slackdump failed: %v\n", err)
		return nil, nil
	}
	return sd, provider
}
