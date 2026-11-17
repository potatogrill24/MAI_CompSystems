package catalog

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"mai/compsystems/internal/httpx"
	"mai/compsystems/internal/model"
)

func dbError(w http.ResponseWriter, err error) {
	var pg *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, 404, "not found")
	} else if errors.As(err, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
		httpx.Error(w, 409, "conflicting record")
	} else {
		httpx.Internal(w, err)
	}
}
func Products(ctx context.Context, db *pgxpool.Pool) (http.Handler, error) {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS products(code VARCHAR(64) PRIMARY KEY,name VARCHAR(255) NOT NULL,weight NUMERIC NOT NULL CHECK(weight>0),description TEXT NOT NULL)`)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	httpx.Health(mux, db.Ping)
	scan := func(row pgx.Row) (model.Product, error) {
		var p model.Product
		err := row.Scan(&p.Code, &p.Name, &p.Weight, &p.Description)
		return p, err
	}
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(r.Context(), "SELECT code,name,weight,description FROM products ORDER BY code")
		if err != nil {
			dbError(w, err)
			return
		}
		defer rows.Close()
		list := []model.Product{}
		for rows.Next() {
			p, e := scan(rows)
			if e != nil {
				dbError(w, e)
				return
			}
			list = append(list, p)
		}
		if err = rows.Err(); err != nil {
			dbError(w, err)
			return
		}
		httpx.JSON(w, 200, list)
	})
	mux.HandleFunc("GET /products/{code}", func(w http.ResponseWriter, r *http.Request) {
		p, err := scan(db.QueryRow(r.Context(), "SELECT code,name,weight,description FROM products WHERE code=$1", r.PathValue("code")))
		if err != nil {
			dbError(w, err)
			return
		}
		httpx.JSON(w, 200, p)
	})
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var p model.Product
			if !httpx.Decode(w, r, &p) {
				return
			}
			if !create {
				if p.Code != "" && p.Code != r.PathValue("code") {
					httpx.Error(w, 400, "code is immutable")
					return
				}
				p.Code = r.PathValue("code")
			}
			if !httpx.Code(p.Code) || !httpx.Text(p.Name) || p.Weight <= 0 || p.Weight > 1e12 || len(p.Description) > 10000 {
				httpx.Error(w, 400, "invalid product fields")
				return
			}
			query := "INSERT INTO products(code,name,weight,description) VALUES($1,$2,$3,$4)"
			status := 201
			if !create {
				query = "UPDATE products SET name=$2,weight=$3,description=$4 WHERE code=$1"
				status = 200
			}
			tag, err := db.Exec(r.Context(), query, p.Code, p.Name, p.Weight, p.Description)
			if err != nil {
				dbError(w, err)
				return
			}
			if tag.RowsAffected() == 0 {
				httpx.Error(w, 404, "product not found")
				return
			}
			w.Header().Set("Location", "/products/"+p.Code)
			httpx.JSON(w, status, p)
		}
	}
	mux.HandleFunc("POST /products", save(true))
	mux.HandleFunc("PUT /products/{code}", save(false))
	mux.HandleFunc("DELETE /products/{code}", func(w http.ResponseWriter, r *http.Request) {
		tag, err := db.Exec(r.Context(), "DELETE FROM products WHERE code=$1", r.PathValue("code"))
		if err != nil {
			dbError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			httpx.Error(w, 404, "product not found")
			return
		}
		w.WriteHeader(204)
	})
	return httpx.Wrap(mux), nil
}

func Inventory(ctx context.Context, db *pgxpool.Pool) (http.Handler, error) {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS stock(code VARCHAR(64) PRIMARY KEY,name VARCHAR(255) NOT NULL,quantity BIGINT NOT NULL CHECK(quantity>=0),cost NUMERIC(14,2) NOT NULL CHECK(cost>=0));CREATE TABLE IF NOT EXISTS reservations(order_code VARCHAR(64) PRIMARY KEY,items JSONB NOT NULL,released BOOLEAN NOT NULL DEFAULT FALSE)`)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	httpx.Health(mux, db.Ping)
	scan := func(row pgx.Row) (model.Stock, error) {
		var s model.Stock
		err := row.Scan(&s.Code, &s.Name, &s.Quantity, &s.Cost)
		return s, err
	}
	mux.HandleFunc("GET /inventory", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(r.Context(), "SELECT code,name,quantity,cost::text FROM stock ORDER BY code")
		if err != nil {
			dbError(w, err)
			return
		}
		defer rows.Close()
		list := []model.Stock{}
		for rows.Next() {
			s, e := scan(rows)
			if e != nil {
				dbError(w, e)
				return
			}
			list = append(list, s)
		}
		if err = rows.Err(); err != nil {
			dbError(w, err)
			return
		}
		httpx.JSON(w, 200, list)
	})
	mux.HandleFunc("GET /inventory/{code}", func(w http.ResponseWriter, r *http.Request) {
		s, err := scan(db.QueryRow(r.Context(), "SELECT code,name,quantity,cost::text FROM stock WHERE code=$1", r.PathValue("code")))
		if err != nil {
			dbError(w, err)
			return
		}
		httpx.JSON(w, 200, s)
	})
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var s model.Stock
			if !httpx.Decode(w, r, &s) {
				return
			}
			if !create {
				if s.Code != "" && s.Code != r.PathValue("code") {
					httpx.Error(w, 400, "code is immutable")
					return
				}
				s.Code = r.PathValue("code")
			}
			if !httpx.Code(s.Code) || !httpx.Text(s.Name) || s.Quantity < 0 || s.Quantity > 1_000_000_000 || !httpx.Money(s.Cost) {
				httpx.Error(w, 400, "invalid stock fields; cost must be a non-negative decimal string")
				return
			}
			query := "INSERT INTO stock(code,name,quantity,cost) VALUES($1,$2,$3,$4)"
			status := 201
			if !create {
				query = "UPDATE stock SET name=$2,quantity=$3,cost=$4 WHERE code=$1"
				status = 200
			}
			tag, err := db.Exec(r.Context(), query, s.Code, s.Name, s.Quantity, s.Cost)
			if err != nil {
				dbError(w, err)
				return
			}
			if tag.RowsAffected() == 0 {
				httpx.Error(w, 404, "stock not found")
				return
			}
			httpx.JSON(w, status, s)
		}
	}
	mux.HandleFunc("POST /inventory", save(true))
	mux.HandleFunc("PUT /inventory/{code}", save(false))
	mux.HandleFunc("DELETE /inventory/{code}", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.Begin(r.Context())
		if err != nil {
			dbError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		var code string
		err = tx.QueryRow(r.Context(), "SELECT code FROM stock WHERE code=$1 FOR UPDATE", r.PathValue("code")).Scan(&code)
		if err != nil {
			dbError(w, err)
			return
		}
		var active bool
		err = tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM reservations r, jsonb_array_elements(r.items) item WHERE NOT r.released AND item->>'product_code'=$1)`, code).Scan(&active)
		if err != nil {
			dbError(w, err)
			return
		}
		if active {
			httpx.Error(w, 409, "stock has active reservations")
			return
		}
		_, err = tx.Exec(r.Context(), "DELETE FROM stock WHERE code=$1", code)
		if err == nil {
			err = tx.Commit(r.Context())
		}
		if err != nil {
			dbError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /reservations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			OrderCode string       `json:"order_code"`
			Items     []model.Item `json:"items"`
		}
		if !httpx.Decode(w, r, &in) {
			return
		}
		if !httpx.Code(in.OrderCode) || !ValidItems(in.Items) {
			httpx.Error(w, 400, "invalid reservation")
			return
		}
		sort.Slice(in.Items, func(i, j int) bool { return in.Items[i].ProductCode < in.Items[j].ProductCode })
		items, err := reserve(r.Context(), db, in.OrderCode, in.Items)
		if err != nil {
			var conflict *conflictError
			if errors.As(err, &conflict) {
				httpx.Error(w, 409, err.Error())
			} else {
				dbError(w, err)
			}
			return
		}
		httpx.JSON(w, 200, items)
	})
	mux.HandleFunc("DELETE /reservations/{code}", func(w http.ResponseWriter, r *http.Request) {
		if !httpx.Code(r.PathValue("code")) {
			httpx.Error(w, 400, "invalid code")
			return
		}
		if err := release(r.Context(), db, r.PathValue("code")); err != nil {
			dbError(w, err)
			return
		}
		w.WriteHeader(204)
	})
	return httpx.Wrap(mux), nil
}
func ValidItems(items []model.Item) bool {
	if len(items) == 0 || len(items) > 100 {
		return false
	}
	seen := map[string]bool{}
	for _, i := range items {
		if !httpx.Code(i.ProductCode) || i.Quantity < 1 || i.Quantity > 1_000_000 || seen[i.ProductCode] {
			return false
		}
		seen[i.ProductCode] = true
	}
	return true
}
