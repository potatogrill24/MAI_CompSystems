package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"mai/compsystems/internal/model"
)

type conflictError struct{ message string }

func (e *conflictError) Error() string { return e.message }
func reserve(ctx context.Context, db *pgxpool.Pool, code string, items []model.Item) ([]model.Item, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", code); err != nil {
		return nil, err
	}
	var raw []byte
	var released bool
	err = tx.QueryRow(ctx, "SELECT items,released FROM reservations WHERE order_code=$1", code).Scan(&raw, &released)
	if err == nil {
		if released {
			return nil, &conflictError{"reservation already released"}
		}
		var existing []model.Item
		if err = json.Unmarshal(raw, &existing); err != nil {
			return nil, err
		}
		if len(existing) != len(items) {
			return nil, &conflictError{"reservation payload differs"}
		}
		for i := range items {
			if existing[i].ProductCode != items[i].ProductCode || existing[i].Quantity != items[i].Quantity {
				return nil, &conflictError{"reservation payload differs"}
			}
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	for i := range items {
		var quantity int
		err = tx.QueryRow(ctx, "SELECT name,quantity,cost::text FROM stock WHERE code=$1 FOR UPDATE", items[i].ProductCode).Scan(&items[i].Name, &quantity, &items[i].Cost)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && quantity < items[i].Quantity {
			return nil, &conflictError{fmt.Sprintf("insufficient stock for %s", items[i].ProductCode)}
		}
		if err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, "UPDATE stock SET quantity=quantity-$2 WHERE code=$1", items[i].ProductCode, items[i].Quantity); err != nil {
			return nil, err
		}
	}
	raw, err = json.Marshal(items)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO reservations(order_code,items) VALUES($1,$2)", code, raw)
	if err != nil {
		return nil, err
	}
	return items, tx.Commit(ctx)
}
func release(ctx context.Context, db *pgxpool.Pool, code string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", code); err != nil {
		return err
	}
	var raw []byte
	var released bool
	err = tx.QueryRow(ctx, "SELECT items,released FROM reservations WHERE order_code=$1", code).Scan(&raw, &released)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, "INSERT INTO reservations(order_code,items,released) VALUES($1,'[]',true)", code)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if released {
		return tx.Commit(ctx)
	}
	var items []model.Item
	if err = json.Unmarshal(raw, &items); err != nil {
		return err
	}
	for _, item := range items {
		if _, err = tx.Exec(ctx, "UPDATE stock SET quantity=quantity+$2 WHERE code=$1", item.ProductCode, item.Quantity); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE reservations SET released=true WHERE order_code=$1", code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
