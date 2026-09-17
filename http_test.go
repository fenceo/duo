package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBodyRejectsTrailingJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		ok    bool
	}{
		{name: "valid", input: `{"name":"ok"}`, ok: true},
		{name: "valid whitespace", input: `{"name":"ok"}` + "\n\t ", ok: true},
		{name: "second object", input: `{"name":"ok"} {}`, ok: false},
		{name: "second value", input: `{"name":"ok"}null`, ok: false},
		{name: "trailing garbage", input: `{"name":"ok"}garbage`, ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/test", strings.NewReader(tc.input))
			var v struct {
				Name string `json:"name"`
			}
			if got := body(w, r, &v); got != tc.ok {
				t.Fatalf("body() = %v, want %v; response=%s", got, tc.ok, w.Body.String())
			}
			if tc.ok && v.Name != "ok" {
				t.Fatalf("decoded value = %#v", v)
			}
			if !tc.ok && w.Code != 400 {
				t.Fatalf("status = %d, want 400", w.Code)
			}
		})
	}
}
