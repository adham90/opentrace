package web

import "net/http"

const testCSRFToken = "test-csrf-token-for-unit-tests"

// withCSRF adds the CSRF cookie and X-CSRF-Token header to a request.
// Use this for JSON API requests in tests.
func withCSRF(req *http.Request) *http.Request {
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: testCSRFToken})
	req.Header.Set("X-CSRF-Token", testCSRFToken)
	return req
}

// withCSRFForm adds the CSRF cookie to a request and appends the _csrf
// field to the form body. Only works for url-encoded forms.
// The caller should include _csrf in their url.Values instead when using postForm.
func csrfCookie() *http.Cookie {
	return &http.Cookie{Name: csrfCookieName, Value: testCSRFToken}
}
