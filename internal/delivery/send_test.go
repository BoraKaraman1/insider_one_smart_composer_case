package delivery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BoraKaraman1/insider_one_smart_composer_case/internal/retry"
)

func brokenBody(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Length: 100\r\n\r\npartial", status, http.StatusText(status))
		_ = buf.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestPostClassifiesStatusBeforeBody(t *testing.T) {
	cases := []struct {
		status int
		want   retry.Class
	}{
		{http.StatusBadRequest, retry.ClientError},
		{http.StatusUnprocessableEntity, retry.ClientError},
		{http.StatusServiceUnavailable, retry.ServerError},
		{http.StatusOK, retry.ConnReset},
	}
	for _, cs := range cases {
		w := &SendWorker{ProviderURL: brokenBody(t, cs.status)}
		_, res, lastErr := w.post(context.Background(), "m-1", []byte(`{}`))
		if res.Class != cs.want {
			t.Errorf("%d with a broken body: %s (%s), want %s", cs.status, res.Class, lastErr, cs.want)
		}
	}
}
