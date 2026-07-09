package retrieval

import (
	"fmt"
	"io"
	"net/http"
)

func decodeHTTPError(resp *http.Response, action string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if len(body) == 0 {
		return fmt.Errorf("%s failed: status %d", action, resp.StatusCode)
	}
	return fmt.Errorf("%s failed: status %d: %s", action, resp.StatusCode, string(body))
}
