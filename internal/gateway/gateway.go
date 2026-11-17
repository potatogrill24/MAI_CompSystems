package gateway

import (
	"context"
	"fmt"
	"github.com/graphql-go/graphql"
	"mai/compsystems/internal/httpx"
	"net/http"
	"net/url"
)

func Routes() (http.Handler, error) {
	productURL := httpx.Env("PRODUCT_URL", "http://localhost:8083")
	orderURL := httpx.Env("ORDER_URL", "http://localhost:8085")
	scalar := func(t graphql.Output) *graphql.Field { return &graphql.Field{Type: t} }
	call := func(ctx context.Context, method, path string, input any) (any, error) {
		var out any
		err := httpx.Call(ctx, method, path, input, &out)
		return out, err
	}
	codeArg := graphql.FieldConfigArgument{"code": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}}
	product := graphql.NewObject(graphql.ObjectConfig{Name: "Product", Fields: graphql.Fields{"code": scalar(graphql.String), "name": scalar(graphql.String), "weight": scalar(graphql.Float), "description": scalar(graphql.String)}})
	item := graphql.NewObject(graphql.ObjectConfig{Name: "OrderItem", Fields: graphql.Fields{"product_code": scalar(graphql.String), "name": scalar(graphql.String), "quantity": scalar(graphql.Int), "cost": scalar(graphql.String), "product": &graphql.Field{Type: product, Resolve: func(p graphql.ResolveParams) (any, error) {
		source, ok := p.Source.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid order item")
		}
		code, _ := source["product_code"].(string)
		return call(p.Context, "GET", productURL+"/products/"+url.PathEscape(code), nil)
	}}}})
	order := graphql.NewObject(graphql.ObjectConfig{Name: "Order", Fields: graphql.Fields{"code": scalar(graphql.String), "date": scalar(graphql.String), "status": scalar(graphql.String), "items": scalar(graphql.NewList(item))}})
	query := graphql.NewObject(graphql.ObjectConfig{Name: "Query", Fields: graphql.Fields{
		"products": &graphql.Field{Type: graphql.NewList(product), Resolve: func(p graphql.ResolveParams) (any, error) { return call(p.Context, "GET", productURL+"/products", nil) }},
		"product": &graphql.Field{Type: product, Args: codeArg, Resolve: func(p graphql.ResolveParams) (any, error) {
			return call(p.Context, "GET", productURL+"/products/"+url.PathEscape(p.Args["code"].(string)), nil)
		}},
		"orders": &graphql.Field{Type: graphql.NewList(order), Resolve: func(p graphql.ResolveParams) (any, error) { return call(p.Context, "GET", orderURL+"/orders", nil) }},
		"order": &graphql.Field{Type: order, Args: codeArg, Resolve: func(p graphql.ResolveParams) (any, error) {
			return call(p.Context, "GET", orderURL+"/orders/"+url.PathEscape(p.Args["code"].(string)), nil)
		}},
	}})
	productInput := graphql.NewInputObject(graphql.InputObjectConfig{Name: "ProductInput", Fields: graphql.InputObjectConfigFieldMap{"code": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)}, "name": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)}, "weight": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Float)}, "description": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)}}})
	itemInput := graphql.NewInputObject(graphql.InputObjectConfig{Name: "OrderItemInput", Fields: graphql.InputObjectConfigFieldMap{"product_code": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)}, "quantity": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.Int)}}})
	orderInput := graphql.NewInputObject(graphql.InputObjectConfig{Name: "OrderInput", Fields: graphql.InputObjectConfigFieldMap{"code": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)}, "date": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.String)}, "items": &graphql.InputObjectFieldConfig{Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(itemInput)))}}})
	mutation := graphql.NewObject(graphql.ObjectConfig{Name: "Mutation", Fields: graphql.Fields{
		"createProduct": &graphql.Field{Type: product, Args: graphql.FieldConfigArgument{"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(productInput)}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			return call(p.Context, "POST", productURL+"/products", p.Args["input"])
		}},
		"updateProduct": &graphql.Field{Type: product, Args: graphql.FieldConfigArgument{"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(productInput)}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			in := p.Args["input"].(map[string]any)
			return call(p.Context, "PUT", productURL+"/products/"+url.PathEscape(in["code"].(string)), in)
		}},
		"deleteProduct": &graphql.Field{Type: graphql.Boolean, Args: codeArg, Resolve: func(p graphql.ResolveParams) (any, error) {
			err := httpx.Call(p.Context, "DELETE", productURL+"/products/"+url.PathEscape(p.Args["code"].(string)), nil, nil)
			return err == nil, err
		}},
		"createOrder": &graphql.Field{Type: order, Args: graphql.FieldConfigArgument{"input": &graphql.ArgumentConfig{Type: graphql.NewNonNull(orderInput)}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			return call(p.Context, "POST", orderURL+"/orders", p.Args["input"])
		}},
		"updateOrder": &graphql.Field{Type: order, Args: graphql.FieldConfigArgument{"code": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}, "date": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)}}, Resolve: func(p graphql.ResolveParams) (any, error) {
			return call(p.Context, "PATCH", orderURL+"/orders/"+url.PathEscape(p.Args["code"].(string)), map[string]any{"date": p.Args["date"]})
		}},
		"deleteOrder": &graphql.Field{Type: graphql.Boolean, Args: codeArg, Resolve: func(p graphql.ResolveParams) (any, error) {
			err := httpx.Call(p.Context, "DELETE", orderURL+"/orders/"+url.PathEscape(p.Args["code"].(string)), nil, nil)
			return err == nil, err
		}},
	}})
	schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: query, Mutation: mutation})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	httpx.Health(mux, func(ctx context.Context) error {
		if err := httpx.Call(ctx, "GET", productURL+"/readyz", nil, nil); err != nil {
			return err
		}
		return httpx.Call(ctx, "GET", orderURL+"/readyz", nil, nil)
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if !httpx.Decode(w, r, &in) {
			return
		}
		if in.Query == "" {
			httpx.Error(w, 400, "query required")
			return
		}
		result := graphql.Do(graphql.Params{Schema: schema, RequestString: in.Query, VariableValues: in.Variables, OperationName: in.OperationName, Context: r.Context()})
		httpx.JSON(w, 200, result)
	})
	return httpx.Wrap(mux), nil
}
