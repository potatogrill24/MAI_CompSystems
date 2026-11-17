package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"mai/compsystems/internal/catalog"
	"mai/compsystems/internal/httpx"
	"mai/compsystems/internal/model"
)

const Queue = "order.created"

type Service struct {
	collection        *mongo.Collection
	inventory, broker string
	mu                sync.Mutex
}

func Routes(ctx context.Context, db *mongo.Database) (http.Handler, error) {
	s := &Service{collection: db.Collection("orders"), inventory: httpx.Env("INVENTORY_URL", "http://localhost:8084"), broker: httpx.Env("AMQP_URL", "amqp://guest:guest@localhost:5672/")}
	mux := http.NewServeMux()
	httpx.Health(mux, func(c context.Context) error { return db.Client().Ping(c, nil) })
	mux.HandleFunc("GET /orders", func(w http.ResponseWriter, r *http.Request) {
		cur, err := s.collection.Find(r.Context(), bson.M{"status": bson.M{"$ne": "cancelled"}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer cur.Close(r.Context())
		list := []model.Order{}
		if err = cur.All(r.Context(), &list); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, 200, list)
	})
	mux.HandleFunc("GET /orders/{code}", func(w http.ResponseWriter, r *http.Request) {
		var o model.Order
		if err := s.collection.FindOne(r.Context(), bson.M{"_id": r.PathValue("code"), "status": bson.M{"$ne": "cancelled"}}).Decode(&o); err != nil {
			failure(w, err)
			return
		}
		httpx.JSON(w, 200, o)
	})
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Code  string       `json:"code"`
			Date  string       `json:"date"`
			Items []model.Item `json:"items"`
		}
		if !httpx.Decode(w, r, &in) {
			return
		}
		if !httpx.Code(in.Code) || !httpx.Date(in.Date) || !catalog.ValidItems(in.Items) {
			httpx.Error(w, 400, "valid code, date and non-empty unique items required")
			return
		}
		// Price and name always come from Inventory, never from client claims.
		o := model.Order{Code: in.Code, Date: in.Date, Items: in.Items, Status: "pending"}
		_, err := s.collection.InsertOne(r.Context(), o)
		if err != nil {
			failure(w, err)
			return
		}
		s.mu.Lock()
		err = s.process(r.Context(), &o)
		s.mu.Unlock()
		status := 201
		if err != nil {
			status = 202
			slog.Warn("order pending retry", "code", o.Code, "error", err)
		}
		if o.Status == "rejected" {
			httpx.JSON(w, 409, o)
			return
		}
		w.Header().Set("Location", "/orders/"+o.Code)
		httpx.JSON(w, status, o)
	})
	mux.HandleFunc("PATCH /orders/{code}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Date string `json:"date"`
		}
		if !httpx.Decode(w, r, &in) {
			return
		}
		if !httpx.Date(in.Date) {
			httpx.Error(w, 400, "invalid date")
			return
		}
		var o model.Order
		err := s.collection.FindOneAndUpdate(r.Context(), bson.M{"_id": r.PathValue("code"), "status": bson.M{"$in": []string{"pending", "confirmed"}}}, bson.M{"$set": bson.M{"date": in.Date}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&o)
		if err != nil {
			failure(w, err)
			return
		}
		httpx.JSON(w, 200, o)
	})
	mux.HandleFunc("DELETE /orders/{code}", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var o model.Order
		err := s.collection.FindOneAndUpdate(r.Context(), bson.M{"_id": r.PathValue("code"), "status": bson.M{"$ne": "cancelled"}}, bson.M{"$set": bson.M{"status": "cancelling"}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&o)
		if err != nil {
			failure(w, err)
			return
		}
		if err = s.process(r.Context(), &o); err != nil {
			httpx.JSON(w, 202, o)
			return
		}
		w.WriteHeader(204)
	})
	go s.run(ctx)
	return httpx.Wrap(mux), nil
}
func failure(w http.ResponseWriter, err error) {
	if errors.Is(err, mongo.ErrNoDocuments) {
		httpx.Error(w, 404, "order not found or not editable")
	} else if mongo.IsDuplicateKeyError(err) {
		httpx.Error(w, 409, "order code already exists")
	} else {
		httpx.Internal(w, err)
	}
}
func (s *Service) process(ctx context.Context, o *model.Order) error {
	if o.Status == "cancelling" {
		if err := httpx.Call(ctx, "DELETE", s.inventory+"/reservations/"+o.Code, nil, nil); err != nil {
			return err
		}
		_, err := s.collection.UpdateOne(ctx, bson.M{"_id": o.Code, "status": "cancelling"}, bson.M{"$set": bson.M{"status": "cancelled"}})
		if err == nil {
			o.Status = "cancelled"
		}
		return err
	}
	if o.Status != "pending" {
		return nil
	}
	var items []model.Item
	err := httpx.Call(ctx, "POST", s.inventory+"/reservations", map[string]any{"order_code": o.Code, "items": o.Items}, &items)
	status := "confirmed"
	if err != nil {
		var remote *httpx.RemoteError
		if !errors.As(err, &remote) || remote.Status != 409 {
			return err
		}
		status = "rejected"
		items = o.Items
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": o.Code, "status": "pending"}, bson.M{"$set": bson.M{"status": status, "items": items, "event_pending": status == "confirmed"}})
	if err == nil && result.ModifiedCount > 0 {
		o.Status = status
		o.Items = items
	}
	return err
}
func (s *Service) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}
func (s *Service) tick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.collection.Find(ctx, bson.M{"$or": []bson.M{{"status": bson.M{"$in": []string{"pending", "cancelling"}}}, {"event_pending": true}}}, options.Find().SetLimit(100))
	if err != nil {
		slog.Warn("order worker", "error", err)
		return
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var o model.Order
		if err = cur.Decode(&o); err != nil {
			continue
		}
		if err = s.process(ctx, &o); err != nil {
			slog.Warn("order retry", "code", o.Code, "error", err)
			continue
		}
		if o.EventPending {
			if err = s.publish(ctx, o); err != nil {
				slog.Warn("publish retry", "error", err)
				continue
			}
			_, err = s.collection.UpdateOne(ctx, bson.M{"_id": o.Code}, bson.M{"$set": bson.M{"event_pending": false}})
			if err != nil {
				slog.Warn("outbox update", "error", err)
			}
		}
	}
}
func (s *Service) publish(ctx context.Context, o model.Order) error {
	conn, err := amqp.DialConfig(s.broker, amqp.Config{Dial: amqp.DefaultDial(5 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err = ch.QueueDeclare(Queue, true, false, false, false, nil); err != nil {
		return err
	}
	if err = ch.Confirm(false); err != nil {
		return err
	}
	event := model.Notification{ID: o.Code + ":created", Type: "order.created", Description: fmt.Sprintf("Order %s confirmed", o.Code), Date: time.Now().UTC()}
	raw, _ := json.Marshal(event)
	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", Queue, true, false, amqp.Publishing{DeliveryMode: amqp.Persistent, ContentType: "application/json", MessageId: event.ID, Body: raw})
	if err != nil {
		return err
	}
	ok, err := confirmation.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("broker did not acknowledge message")
	}
	return nil
}
