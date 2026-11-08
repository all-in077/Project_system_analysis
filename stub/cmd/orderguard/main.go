// Команда orderguard запускает учебную заглушку с поддельным брокером.
// Она не ходит к реальному брокеру и не торгует.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/all-in077/Project_system_analysis/stub/internal/service"
)

// demoBroker всегда подтверждает заявку; ключ идемпотентности запоминает.
type demoBroker struct{ posted map[string]string }

func (b *demoBroker) PostOrder(_ context.Context, key string, _ service.Params) (string, error) {
	if _, ok := b.posted[key]; ok {
		return "", service.ErrKeyAlreadyUsed
	}
	id := "demo-" + key
	b.posted[key] = id
	return id, nil
}
func (b *demoBroker) ActiveOrders(context.Context) ([]service.BrokerOrder, error) { return nil, nil }

type orderDTO struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	BrokerID string `json:"brokerOrderId,omitempty"`
}

type placeReq struct {
	InstrumentID string `json:"instrumentId"`
	Direction    string `json:"direction"`
	OrderType    string `json:"orderType"`
	QuantityLots string `json:"quantityLots"`
	Price        string `json:"price"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	if a, ok := err.(service.APIError); ok {
		code := http.StatusUnprocessableEntity
		switch a.Code {
		case "IDEMPOTENCY_KEY_REQUIRED", "INVALID_IDEMPOTENCY_KEY":
			code = http.StatusBadRequest
		case "IDEMPOTENCY_KEY_REUSED", "ORDER_NOT_CANCELLABLE":
			code = http.StatusConflict
		case "ORDER_NOT_FOUND":
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"code": a.Code, "message": a.Message})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "INTERNAL", "message": err.Error()})
}

func main() {
	svc := service.New(&demoBroker{posted: map[string]string{}})
	const user = "demo-user" // в реальной системе пользователь определяется по токену сессии

	http.HandleFunc("/v1/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var in placeReq
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 400, map[string]string{"code": "VALIDATION_FAILED", "message": "bad json"})
			return
		}
		o, created, err := svc.Place(r.Context(), user, r.Header.Get("Idempotency-Key"), service.Params{
			InstrumentID: in.InstrumentID, Direction: in.Direction, OrderType: in.OrderType,
			Quantity: in.QuantityLots, Price: in.Price,
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		code := http.StatusOK
		if created {
			code = http.StatusAccepted
		}
		writeJSON(w, code, orderDTO{ID: o.ID, Status: string(o.Status), BrokerID: o.BrokerID})
	})

	http.HandleFunc("/v1/orders/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v1/orders/")
		switch r.Method {
		case http.MethodGet:
			o, err := svc.Get(user, id)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, orderDTO{ID: o.ID, Status: string(o.Status), BrokerID: o.BrokerID})
		case http.MethodDelete:
			o, err := svc.Cancel(id)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 202, orderDTO{ID: o.ID, Status: string(o.Status), BrokerID: o.BrokerID})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	log.Println("OrderGuard stub listening on :8080 (demo broker, no real trading)")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
