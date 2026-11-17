package management

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCalculatorHTTP(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"works":[{"id":1,"task":"A","duration":2,"resource":10}],"iterations":100}`, 200},
		{`{"works":[{"id":1,"task":"A","duration":2,"resource":11}]}`, 400},
		{`null`, 400},
		{`{"works":[],"unknown":true}`, 400},
		{`{} {}`, 400},
		{`{"iterations":-1}`, 400},
	} {
		r := httptest.NewRequest("POST", "/calculate", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		Calculator().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
}
