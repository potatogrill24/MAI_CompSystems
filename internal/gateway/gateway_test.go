package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRESTFederation(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/products/P1":
			_, _ = w.Write([]byte(`{"code":"P1","name":"Изделие","weight":1.5,"description":"text"}`))
		case "/orders/O1":
			_, _ = w.Write([]byte(`{"code":"O1","date":"2026-11-17","status":"confirmed","items":[{"product_code":"P1","quantity":2,"cost":"1.50"}]}`))
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer backend.Close()
	t.Setenv("PRODUCT_URL", backend.URL)
	t.Setenv("ORDER_URL", backend.URL)
	handler, err := Routes()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"query":"query($code:String!){order(code:$code){code items{quantity product{code name}}}}","variables":{"code":"O1"}}`
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/graphql", strings.NewReader(body)))
	var result struct {
		Data struct {
			Order struct {
				Code  string
				Items []struct {
					Quantity int
					Product  struct{ Code, Name string }
				}
			}
		}
		Errors []any
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Errors) > 0 || len(result.Data.Order.Items) != 1 {
		t.Fatal(w.Body.String())
	}
	item := result.Data.Order.Items[0]
	if item.Product.Code != "P1" || item.Product.Name != "Изделие" || item.Quantity != 2 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/graphql", strings.NewReader(`{"query":"{product(code:\"missing\"){code}}"}`)))
	var failure struct{ Errors []any }
	if err = json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if len(failure.Errors) == 0 {
		t.Fatal("upstream error was hidden")
	}
}
