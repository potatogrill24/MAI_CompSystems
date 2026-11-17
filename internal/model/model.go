package model

import "time"

type Work struct {
	ID       int    `json:"id"`
	Task     string `json:"task"`
	Duration int    `json:"duration"`
	Resource int    `json:"resource"`
	Pred     []int  `json:"pred"`
}
type Project struct {
	ID        int    `json:"id"`
	OrderName string `json:"order_name"`
	StartDate string `json:"start_date"`
	Works     []Work `json:"works"`
}
type Product struct {
	Code        string  `json:"code" bson:"code"`
	Name        string  `json:"name" bson:"name"`
	Weight      float64 `json:"weight" bson:"weight"`
	Description string  `json:"description" bson:"description"`
}
type Stock struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
	Cost     string `json:"cost"` // Decimal string: exact monetary value, never a float.
}
type Item struct {
	ProductCode string `json:"product_code" bson:"product_code"`
	Name        string `json:"name" bson:"name"`
	Quantity    int    `json:"quantity" bson:"quantity"`
	Cost        string `json:"cost" bson:"cost"`
}
type Order struct {
	Code         string `json:"code" bson:"_id"`
	Date         string `json:"date" bson:"date"`
	Items        []Item `json:"items" bson:"items"`
	Status       string `json:"status" bson:"status"`
	EventPending bool   `json:"-" bson:"event_pending"`
}
type Notification struct {
	ID          string    `json:"id" bson:"_id"`
	Type        string    `json:"type" bson:"type"`
	Description string    `json:"description" bson:"description"`
	Date        time.Time `json:"date" bson:"date"`
}
