package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeIOExtEntriesStub(t *testing.T) {
	r, _ := http.NewRequest("GET", "/api/extended", nil)
	w := httptest.NewRecorder()
	serveIOExtEntries(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("GET %s: %d; want %d", r.URL.String(), w.Code, http.StatusOK)
	}
	ctype := "application/json;charset=utf-8"
	if w.Header().Get("Content-Type") != ctype {
		t.Errorf("Content-Type: %q; want %q", w.Header().Get("Content-Type"), ctype)
	}
}

func TestServeTemplate(t *testing.T) {
	const ctype = "text/html;charset=utf-8"

	revert := preserveConfig()
	defer revert()
	config.Prefix = "/root"

	table := []struct{ path, slug, canonical string }{
		{"/", "home", "/root/"},
		{"/home?experiment", "home", "/root/"},
		{"/about", "about", "/root/about"},
		{"/about?experiment", "about", "/root/about"},
		{"/schedule", "schedule", "/root/schedule"},
		{"/onsite", "onsite", "/root/onsite"},
		{"/offsite", "offsite", "/root/offsite"},
		{"/registration", "registration", "/root/registration"},
		{"/faq", "faq", "/root/faq"},
		{"/form", "form", "/root/form"},
	}
	for i, test := range table {
		r, _ := http.NewRequest("GET", test.path, nil)
		w := httptest.NewRecorder()
		serveTemplate(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("%d: GET %s = %d; want %d", i, test.path, w.Code, http.StatusOK)
			continue
		}
		if v := w.Header().Get("Content-Type"); v != ctype {
			t.Errorf("%d: Content-Type: %q; want %q", i, v, ctype)
		}
		if w.Header().Get("Cache-Control") == "" {
			t.Errorf("%d: want cache-control header", i)
		}

		body := string(w.Body.String())

		tag := `<body id="page-` + test.slug + `"`
		if !strings.Contains(body, tag) {
			t.Errorf("%d: %s does not contain %s", i, body, tag)
		}
		tag = `<link rel="canonical" href="` + test.canonical + `"`
		if !strings.Contains(body, tag) {
			t.Errorf("%d: %s does not contain %s", i, body, tag)
		}
	}
}

func TestServeTemplateRedirect(t *testing.T) {
	table := []struct{ start, redirect string }{
		{"/about/", "/about"},
		{"/one/two/", "/one/two"},
	}
	for i, test := range table {
		r, _ := http.NewRequest("GET", test.start, nil)
		w := httptest.NewRecorder()
		serveTemplate(w, r)

		if w.Code != http.StatusFound {
			t.Fatalf("%d: GET %s: %d; want %d", i, test.start, w.Code, http.StatusFound)
		}
		redirect := config.Prefix + test.redirect
		if loc := w.Header().Get("Location"); loc != redirect {
			t.Errorf("%d: Location: %q; want %q", i, loc, redirect)
		}
	}
}

func TestServeTemplate404(t *testing.T) {
	r, _ := http.NewRequest("GET", "/a-thing-that-is-not-there", nil)
	w := httptest.NewRecorder()
	serveTemplate(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET %s: %d; want %d", r.URL.String(), w.Code, http.StatusNotFound)
	}
	const ctype = "text/html;charset=utf-8"
	if v := w.Header().Get("Content-Type"); v != ctype {
		t.Errorf("Content-Type: %q; want %q", v, ctype)
	}
	if v := w.Header().Get("Cache-Control"); v != "" {
		t.Errorf("don't want Cache-Control: %q", v)
	}
}

func TestHandleAuth(t *testing.T) {
	defer preserveConfig()()
	const code = "fake-auth-code"

	table := []struct {
		token            string
		doExchange       bool
		exchangeRespCode int
		success          bool
	}{
		{"valid", true, http.StatusOK, true},
		{"valid", true, http.StatusBadRequest, false},
		{"", false, http.StatusOK, false},
		{testIDToken, true, http.StatusOK, true},
		{testIDToken, true, http.StatusBadRequest, false},
	}

	for i, test := range table {
		done := make(chan struct{}, 1)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if v := r.FormValue("code"); v != code {
				t.Errorf("code = %q; want %q", v, code)
			}
			if v := r.FormValue("client_id"); v != testClientID {
				t.Errorf("client_id = %q; want %q", v, testClientID)
			}
			if v := r.FormValue("client_secret"); v != testClientSecret {
				t.Errorf("client_secret = %q; want %q", v, testClientSecret)
			}
			if v := r.FormValue("redirect_uri"); v != "postmessage" {
				t.Errorf("redirect_uri = %q; want postmessage", v)
			}
			if v := r.FormValue("grant_type"); v != "authorization_code" {
				t.Errorf("grant_type = %q; want authorization_code", v)
			}

			w.WriteHeader(test.exchangeRespCode)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{
				"access_token": "new-access-token",
				"refresh_token": "new-refresh-token",
				"id_token": %q,
				"expires_in": 3600
			}`, testIDToken)

			done <- struct{}{}
		}))
		defer ts.Close()
		config.Google.TokenURL = ts.URL

		p := strings.NewReader(`{"code": "` + code + `"}`)
		r, _ := http.NewRequest("POST", "/api/v1/auth", p)
		r.Header.Set("Authorization", "Bearer "+test.token)
		w := httptest.NewRecorder()

		cache.flush(newContext(r))
		handleAuth(w, r)

		if test.success && w.Code != http.StatusOK {
			t.Errorf("%d: code = %d; want 200\nbody: %s", i, w.Code, w.Body.String())
		} else if !test.success && w.Code == http.StatusOK {
			t.Errorf("%d: code = 200; want > 399\nbody: %s", i, w.Body)
		}

		select {
		case <-done:
			if !test.doExchange {
				t.Errorf("%d: should not have done code exchange", i)
			}
		default:
			if test.doExchange {
				t.Errorf("code exchange never happened")
			}
		}
	}
}
