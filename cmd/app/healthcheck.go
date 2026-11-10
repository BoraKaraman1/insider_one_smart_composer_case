package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// healthcheckTimeout bounds the one request of "app healthcheck".
const healthcheckTimeout = 2 * time.Second

// healthcheckCmd is "app healthcheck <url>" for compose healthchecks: the
// image is distroless, with no shell or curl. One GET; exit 0 on 200, 1 on
// anything else (another status, a redirect, an error, the timeout), 2 on
// a usage error.
func healthcheckCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: app healthcheck <url>")
		return 2
	}
	c := &http.Client{
		Timeout: healthcheckTimeout,
		// A redirect is not healthy: the answer must be the 200 itself.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Get(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
