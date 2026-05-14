package openaicompat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cfbender/hygge/internal/provider"
)

// readHTTPError consumes a non-2xx response body and returns a typed,
// classified error.  The response body is read and closed regardless.
func (a *adapter) readHTTPError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)

	var detail apiErrorResponse
	_ = json.Unmarshal(raw, &detail)

	msg := detail.Error.Message
	if msg == "" {
		msg = string(raw)
	}
	if msg == "" {
		msg = resp.Status
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s: %s", provider.ErrAuth, a.cfg.Name, msg)
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusNotFound:
		return fmt.Errorf("%w: %s: %s", provider.ErrInvalidRequest, a.cfg.Name, msg)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s: %s", provider.ErrRateLimited, a.cfg.Name, msg)
	default:
		if resp.StatusCode >= 500 {
			return fmt.Errorf("%w: %s: %d %s", provider.ErrTransient, a.cfg.Name, resp.StatusCode, msg)
		}
		return fmt.Errorf("%s: HTTP %d: %s", a.cfg.Name, resp.StatusCode, msg)
	}
}
