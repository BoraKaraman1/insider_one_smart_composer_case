package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

const healthcheckTimeout = 2 * time.Second

// Kabuksuz imajda sağlık kontrolü bu komutla yapılır.
func healthcheckCmd(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: app healthcheck <url>")
		return 2
	}
	c := &http.Client{
		Timeout: healthcheckTimeout,
		// Yönlendirme sağlıklı sayılmaz.
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
