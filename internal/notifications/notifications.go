package notifications

import (
	"context"
	"encoding/json"
	"errors"
	amqp "github.com/rabbitmq/amqp091-go"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"log/slog"
	"mai/compsystems/internal/httpx"
	"mai/compsystems/internal/model"
	"mai/compsystems/internal/orders"
	"net/http"
	"time"
)

func Routes(ctx context.Context, db *mongo.Database) (http.Handler, error) {
	col := db.Collection("notifications")
	mux := http.NewServeMux()
	httpx.Health(mux, func(c context.Context) error { return db.Client().Ping(c, nil) })
	mux.HandleFunc("GET /notifications", func(w http.ResponseWriter, r *http.Request) {
		cur, err := col.Find(r.Context(), bson.M{}, options.Find().SetSort(bson.D{{Key: "date", Value: -1}}))
		if err != nil {
			httpx.Internal(w, err)
			return
		}
		defer cur.Close(r.Context())
		list := []model.Notification{}
		if err = cur.All(r.Context(), &list); err != nil {
			httpx.Internal(w, err)
			return
		}
		httpx.JSON(w, 200, list)
	})
	mux.HandleFunc("GET /notifications/{id}", func(w http.ResponseWriter, r *http.Request) {
		var n model.Notification
		err := col.FindOne(r.Context(), bson.M{"_id": r.PathValue("id")}).Decode(&n)
		if err != nil {
			failure(w, err)
			return
		}
		httpx.JSON(w, 200, n)
	})
	mux.HandleFunc("POST /notifications", func(w http.ResponseWriter, r *http.Request) {
		var n model.Notification
		if !httpx.Decode(w, r, &n) {
			return
		}
		if !httpx.Code(n.ID) || !httpx.Text(n.Type) || !httpx.Text(n.Description) {
			httpx.Error(w, 400, "id, type and description required")
			return
		}
		if n.Date.IsZero() {
			n.Date = time.Now().UTC()
		}
		if _, err := col.InsertOne(r.Context(), n); err != nil {
			failure(w, err)
			return
		}
		httpx.JSON(w, 201, n)
	})

	mux.HandleFunc("PUT /notifications/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Type        string    `json:"type"`
			Description string    `json:"description"`
			Date        time.Time `json:"date"`
		}
		if !httpx.Decode(w, r, &in) {
			return
		}
		if !httpx.Text(in.Type) || !httpx.Text(in.Description) || in.Date.IsZero() {
			httpx.Error(w, 400, "type, description and RFC3339 date required")
			return
		}
		n := model.Notification{ID: r.PathValue("id"), Type: in.Type, Description: in.Description, Date: in.Date}
		res, err := col.ReplaceOne(r.Context(), bson.M{"_id": n.ID}, n)
		if err != nil {
			failure(w, err)
			return
		}
		if res.MatchedCount == 0 {
			httpx.Error(w, 404, "notification not found")
			return
		}
		httpx.JSON(w, 200, n)
	})
	mux.HandleFunc("DELETE /notifications/{id}", func(w http.ResponseWriter, r *http.Request) {
		res, err := col.DeleteOne(r.Context(), bson.M{"_id": r.PathValue("id")})
		if err != nil {
			failure(w, err)
			return
		}
		if res.DeletedCount == 0 {
			httpx.Error(w, 404, "notification not found")
			return
		}
		w.WriteHeader(204)
	})
	go func() {
		for ctx.Err() == nil {
			if err := consume(ctx, col); err != nil {
				slog.Warn("notification consumer reconnecting", "error", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	return httpx.Wrap(mux), nil
}
func failure(w http.ResponseWriter, err error) {
	if errors.Is(err, mongo.ErrNoDocuments) {
		httpx.Error(w, 404, "notification not found")
	} else if mongo.IsDuplicateKeyError(err) {
		httpx.Error(w, 409, "notification already exists")
	} else {
		httpx.Internal(w, err)
	}
}
func consume(ctx context.Context, col *mongo.Collection) error {
	conn, err := amqp.DialConfig(httpx.Env("AMQP_URL", "amqp://guest:guest@localhost:5672/"), amqp.Config{Dial: amqp.DefaultDial(5 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err = ch.QueueDeclare(orders.Queue, true, false, false, false, nil); err != nil {
		return err
	}
	if err = ch.Qos(16, 0, false); err != nil {
		return err
	}
	messages, err := ch.Consume(orders.Queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-messages:
			if !ok {
				return errors.New("consumer channel closed")
			}
			var n model.Notification
			if err = json.Unmarshal(msg.Body, &n); err != nil || n.ID == "" || n.Type == "" {
				_ = msg.Reject(false)
				continue
			}
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err = col.UpdateOne(c, bson.M{"_id": n.ID}, bson.M{"$setOnInsert": n}, options.UpdateOne().SetUpsert(true))
			cancel()
			if err != nil {
				_ = msg.Nack(false, true)
			} else {
				_ = msg.Ack(false)
			}
		}
	}
}
