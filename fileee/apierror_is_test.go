package fileee

import (
	"errors"
	"net/http"
	"testing"
)

func TestAPIError_Is(t *testing.T) {
	cases := []struct {
		name   string
		err    *APIError
		target error
		want   bool
	}{
		{"429 -> RateLimited", &APIError{HTTPStatus: http.StatusTooManyRequests}, ErrRateLimited, true},
		{"415 -> UnsupportedFileType", &APIError{HTTPStatus: http.StatusUnsupportedMediaType}, ErrUnsupportedFileType, true},
		{"code UNSUPPORTED_FILE_TYPE -> UnsupportedFileType", &APIError{HTTPStatus: 400, Code: "UNSUPPORTED_FILE_TYPE"}, ErrUnsupportedFileType, true},
		{"404 -> NotFound", &APIError{HTTPStatus: http.StatusNotFound}, ErrNotFound, true},
		{"500 -> not RateLimited", &APIError{HTTPStatus: 500}, ErrRateLimited, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := errors.Is(c.err, c.target); got != c.want {
				t.Errorf("errors.Is(%+v, %v) = %v, erwartet %v", c.err, c.target, got, c.want)
			}
		})
	}
}
