// Package service реализует ядро OrderGuard: идемпотентное выставление заявок,
// применение событий исполнения, сверку и уведомления. Брокер подключается через интерфейс.
package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/all-in077/Project_system_analysis/stub/internal/domain"
)

// ---- Ошибки брокера (адаптер переводит ответы T-Invest API в эти значения) ----

var (
	// ErrTimeout: ответ не получен, итог неизвестен.
	ErrTimeout = errors.New("broker timeout")
	// ErrKeyAlreadyUsed соответствует ошибке 30057: ключ идемпотентности уже использован.
	ErrKeyAlreadyUsed = errors.New("broker: idempotency key already used")
)

// RejectError: брокер явно отказал.
type RejectError struct{ Reason string }

func (e RejectError) Error() string { return "broker rejected: " + e.Reason }

// ---- Ошибки собственного API ----

type APIError struct{ Code, Message string }

func (e APIError) Error() string { return e.Code + ": " + e.Message }

func apiErr(code, msg string) error { return APIError{code, msg} }

// ---- Модель ----

type Params struct {
	InstrumentID string
	Direction    string // BUY | SELL
	OrderType    string // LIMIT | MARKET
	Quantity     string // десятичная строка
	Price        string // десятичная строка, пусто для MARKET
	IsFuture     bool
}

type Order struct {
	ID        string
	UserID    string
	Key       string
	Params    Params
	BrokerID  string
	Status    domain.Status
	Filled    *big.Rat
	Reason    string
	UpdatedAt time.Time
	trades    map[string]bool
}

type Notification struct {
	OrderID  string
	Kind     string
	DedupKey string
}

// BrokerOrder — активная заявка у брокера, как её видит сверка.
type BrokerOrder struct {
	BrokerID string
	Key      string // order_request_id, ключ идемпотентности
	Filled   *big.Rat
}

type Broker interface {
	PostOrder(ctx context.Context, key string, p Params) (brokerID string, err error)
	ActiveOrders(ctx context.Context) ([]BrokerOrder, error)
}

// LargeOrderLimit — порог подтверждения по СМС (30 млн рублей), см. docs/05.
var LargeOrderLimit = big.NewRat(30_000_000, 1)

// LotSize в заглушке равен 1; в реальной системе берётся из справочника инструментов.
var LotSize = big.NewRat(1, 1)

type Service struct {
	mu     sync.Mutex
	broker Broker
	seq    int
	orders map[string]*Order // по внутреннему id
	byKey  map[string]*Order // по (user, key)
	notes  []Notification
	seen   map[string]bool // дедупликация уведомлений
	Now    func() time.Time
}

func New(b Broker) *Service {
	return &Service{
		broker: b,
		orders: map[string]*Order{},
		byKey:  map[string]*Order{},
		seen:   map[string]bool{},
		Now:    time.Now,
	}
}

// Validate проверяет ограничения до отправки брокеру (ORD-6, ORD-7).
func Validate(p Params) error {
	q, ok := new(big.Rat).SetString(p.Quantity)
	if !ok || q.Sign() <= 0 {
		return apiErr("VALIDATION_FAILED", "quantity must be a positive number")
	}
	if p.Direction != "BUY" && p.Direction != "SELL" {
		return apiErr("VALIDATION_FAILED", "direction must be BUY or SELL")
	}
	if p.InstrumentID == "" {
		return apiErr("VALIDATION_FAILED", "instrumentId is required")
	}
	switch p.OrderType {
	case "LIMIT":
		pr, ok := new(big.Rat).SetString(p.Price)
		if !ok {
			return apiErr("VALIDATION_FAILED", "price is required for LIMIT")
		}
		if pr.Sign() < 0 && !p.IsFuture {
			return apiErr("NEGATIVE_PRICE_NOT_ALLOWED", "negative price is allowed only for futures")
		}
		sum := new(big.Rat).Mul(new(big.Rat).Mul(pr, q), LotSize)
		if sum.Cmp(LargeOrderLimit) > 0 {
			return apiErr("ORDER_REQUIRES_CONFIRMATION", "order above limit requires SMS confirmation, split it")
		}
	case "MARKET":
		if p.Price != "" {
			return apiErr("VALIDATION_FAILED", "price must be empty for MARKET")
		}
		// Оценка суммы рыночной заявки не определена (OQ-6) — проверку порога здесь не делаем.
	default:
		return apiErr("VALIDATION_FAILED", "orderType must be LIMIT or MARKET")
	}
	return nil
}

func keyOf(user, key string) string { return user + "|" + key }

// Place выставляет заявку идемпотентно. created=false означает повтор того же запроса.
func (s *Service) Place(ctx context.Context, user, key string, p Params) (o *Order, created bool, err error) {
	if key == "" {
		return nil, false, apiErr("IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required")
	}

	s.mu.Lock()
	if ex, ok := s.byKey[keyOf(user, key)]; ok {
		defer s.mu.Unlock()
		if ex.Params != p {
			return nil, false, apiErr("IDEMPOTENCY_KEY_REUSED", "key was used with different parameters")
		}
		return ex, false, nil
	}
	if err := Validate(p); err != nil {
		s.mu.Unlock()
		return nil, false, err
	}
	s.seq++
	o = &Order{
		ID: fmt.Sprintf("ord-%d", s.seq), UserID: user, Key: key, Params: p,
		Status: domain.Created, Filled: new(big.Rat), UpdatedAt: s.Now(),
		trades: map[string]bool{},
	}
	s.orders[o.ID] = o
	s.byKey[keyOf(user, key)] = o
	// Сначала записываем SENDING, потом идём к брокеру (docs/04, принципы интеграции).
	s.moveLocked(o, domain.Sending)
	s.mu.Unlock()

	brokerID, err := s.broker.PostOrder(ctx, key, p)

	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err == nil:
		o.BrokerID = brokerID
		s.moveLocked(o, domain.New)
	case errors.Is(err, ErrTimeout):
		s.moveLocked(o, domain.Unconfirmed)
	case errors.Is(err, ErrKeyAlreadyUsed):
		// Заявка с таким ключом уже есть у брокера: уточняем сверкой.
		s.moveLocked(o, domain.Unconfirmed)
	default:
		var rej RejectError
		if errors.As(err, &rej) {
			o.Reason = rej.Reason
			s.moveLocked(o, domain.Rejected)
		} else {
			s.moveLocked(o, domain.Unconfirmed)
		}
	}
	return o, true, nil
}

// moveLocked применяет переход, если он допустим, и создаёт уведомление для значимых состояний.
func (s *Service) moveLocked(o *Order, to domain.Status) bool {
	if o.Status == to && to != domain.PartiallyFilled {
		return false
	}
	if !domain.CanTransition(o.Status, to) {
		return false // событие после терминального или недопустимый переход игнорируется
	}
	o.Status = to
	o.UpdatedAt = s.Now()
	switch to {
	case domain.Filled, domain.Rejected, domain.Cancelled:
		s.notifyLocked(o, string(to), o.ID+"|"+string(to))
	}
	return true
}

func (s *Service) notifyLocked(o *Order, kind, dedup string) {
	if s.seen[dedup] {
		return
	}
	s.seen[dedup] = true
	s.notes = append(s.notes, Notification{OrderID: o.ID, Kind: kind, DedupKey: dedup})
}

// ApplyFill применяет событие исполнения из стрима. Повторная доставка не меняет состояние.
func (s *Service) ApplyFill(orderID, tradeID string, lots string, total string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return apiErr("ORDER_NOT_FOUND", orderID)
	}
	if o.trades[tradeID] {
		return nil // дубликат
	}
	if o.Status.IsTerminal() {
		return nil // правило 1
	}
	l, ok1 := new(big.Rat).SetString(lots)
	q, ok2 := new(big.Rat).SetString(o.Params.Quantity)
	if !ok1 || !ok2 || l.Sign() <= 0 {
		return apiErr("VALIDATION_FAILED", "bad fill")
	}
	o.trades[tradeID] = true
	o.Filled = new(big.Rat).Add(o.Filled, l)
	if o.Filled.Cmp(q) >= 0 {
		s.moveLocked(o, domain.Filled)
	} else {
		s.moveLocked(o, domain.PartiallyFilled)
	}
	return nil
}

// Cancel запрашивает отмену активной заявки (ORD-3).
func (s *Service) Cancel(orderID string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return nil, apiErr("ORDER_NOT_FOUND", orderID)
	}
	if o.Status == domain.CancelRequested {
		return o, nil // повтор отмены идемпотентен
	}
	if o.Status.IsTerminal() || !domain.CanTransition(o.Status, domain.CancelRequested) {
		return nil, apiErr("ORDER_NOT_CANCELLABLE", string(o.Status))
	}
	s.moveLocked(o, domain.CancelRequested)
	return o, nil
}

// ConfirmCancel вызывается, когда брокер подтвердил отмену.
func (s *Service) ConfirmCancel(orderID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.orders[orderID]; ok {
		s.moveLocked(o, domain.Cancelled)
	}
}

// Reconcile сверяет заявки в UNCONFIRMED с активными заявками у брокера (ORD-4, TRK-2).
// Возвращает число разрешённых заявок.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	active, err := s.broker.ActiveOrders(ctx)
	if err != nil {
		return 0, err
	}
	byKey := map[string]BrokerOrder{}
	for _, a := range active {
		byKey[a.Key] = a
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resolved := 0
	for _, o := range s.orders {
		if o.Status != domain.Unconfirmed {
			continue
		}
		a, ok := byKey[o.Key]
		if !ok {
			// Не найдена среди активных: могла исполниться и исчезнуть. Остаётся UNCONFIRMED
			// до ручного разбора (docs/02, S-3.1; OQ-1).
			continue
		}
		o.BrokerID = a.BrokerID
		q, _ := new(big.Rat).SetString(o.Params.Quantity)
		switch {
		case a.Filled != nil && a.Filled.Cmp(q) >= 0:
			o.Filled = new(big.Rat).Set(a.Filled)
			s.moveLocked(o, domain.Filled)
		case a.Filled != nil && a.Filled.Sign() > 0:
			o.Filled = new(big.Rat).Set(a.Filled)
			s.moveLocked(o, domain.PartiallyFilled)
		default:
			s.moveLocked(o, domain.New)
		}
		resolved++
	}
	return resolved, nil
}

// Get возвращает заявку пользователя; чужая заявка выглядит как несуществующая.
func (s *Service) Get(user, id string) (*Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok || o.UserID != user {
		return nil, apiErr("ORDER_NOT_FOUND", id)
	}
	return o, nil
}

// Notifications возвращает копию списка уведомлений.
func (s *Service) Notifications() []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Notification(nil), s.notes...)
}
