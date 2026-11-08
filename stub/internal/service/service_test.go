package service

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/all-in077/Project_system_analysis/stub/internal/domain"
)

// fakeBroker имитирует поведение брокера: идемпотентность по ключу, таймаут, отказ.
type fakeBroker struct {
	posted      map[string]string // key -> brokerID
	calls       int
	failTimeout int // сколько первых вызовов завершить таймаутом (заявка при этом У БРОКЕРА создаётся)
	rejectWith  string
	filled      map[string]*big.Rat
}

func newBroker() *fakeBroker {
	return &fakeBroker{posted: map[string]string{}, filled: map[string]*big.Rat{}}
}

func (b *fakeBroker) PostOrder(_ context.Context, key string, _ Params) (string, error) {
	b.calls++
	if b.rejectWith != "" {
		return "", RejectError{b.rejectWith}
	}
	_, exists := b.posted[key]
	if !exists {
		b.posted[key] = "brk-" + key // заявка создана
	}
	if b.failTimeout > 0 {
		b.failTimeout--
		return "", ErrTimeout // ответ потерян, хотя заявка у брокера есть
	}
	if exists {
		return "", ErrKeyAlreadyUsed // 30057
	}
	return b.posted[key], nil
}

func (b *fakeBroker) ActiveOrders(context.Context) ([]BrokerOrder, error) {
	var out []BrokerOrder
	for k, id := range b.posted {
		out = append(out, BrokerOrder{BrokerID: id, Key: k, Filled: b.filled[k]})
	}
	return out, nil
}

var ctx = context.Background()

func limitBuy() Params {
	return Params{InstrumentID: "BBG004730N88", Direction: "BUY", OrderType: "LIMIT", Quantity: "10", Price: "250.50"}
}

func code(err error) string {
	var a APIError
	if errors.As(err, &a) {
		return a.Code
	}
	return ""
}

func TestPlaceOrder_Accepted(t *testing.T) {
	s := New(newBroker())
	o, created, err := s.Place(ctx, "u1", "k1", limitBuy())
	if err != nil || !created || o.Status != domain.New || o.BrokerID == "" {
		t.Fatalf("got %+v created=%v err=%v", o, created, err)
	}
}

func TestPlaceOrder_IdempotentRepeat(t *testing.T) {
	b := newBroker()
	s := New(b)
	o1, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	o2, created, err := s.Place(ctx, "u1", "k1", limitBuy())
	if err != nil || created || o1.ID != o2.ID {
		t.Fatalf("повтор должен вернуть ту же заявку: %+v %v %v", o2, created, err)
	}
	if b.calls != 1 {
		t.Fatalf("брокеру должен уйти один вызов, got %d", b.calls)
	}
}

func TestPlaceOrder_KeyReusedWithOtherParams(t *testing.T) {
	s := New(newBroker())
	s.Place(ctx, "u1", "k1", limitBuy())
	p := limitBuy()
	p.Quantity = "11"
	_, _, err := s.Place(ctx, "u1", "k1", p)
	if code(err) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("want IDEMPOTENCY_KEY_REUSED, got %v", err)
	}
}

func TestPlaceOrder_KeyRequired(t *testing.T) {
	s := New(newBroker())
	_, _, err := s.Place(ctx, "u1", "", limitBuy())
	if code(err) != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("got %v", err)
	}
}

func TestPlaceOrder_SameKeyDifferentUsersAreIndependent(t *testing.T) {
	s := New(newBroker())
	a, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	b, created, _ := s.Place(ctx, "u2", "k1", limitBuy())
	if !created || a.ID == b.ID {
		t.Fatal("ключ уникален в рамках пользователя")
	}
}

func TestPlaceOrder_TimeoutBecomesUnconfirmed(t *testing.T) {
	b := newBroker()
	b.failTimeout = 1
	s := New(b)
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	if o.Status != domain.Unconfirmed {
		t.Fatalf("want UNCONFIRMED, got %s", o.Status)
	}
}

func TestReconcile_ResolvesUnconfirmed(t *testing.T) {
	b := newBroker()
	b.failTimeout = 1 // заявка у брокера создана, ответ потерян
	s := New(b)
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	n, err := s.Reconcile(ctx)
	if err != nil || n != 1 || o.Status != domain.New {
		t.Fatalf("resolved=%d status=%s err=%v", n, o.Status, err)
	}
	if len(b.posted) != 1 {
		t.Fatal("у брокера должна быть одна заявка")
	}
}

func TestReconcile_FindsPartialAndFull(t *testing.T) {
	b := newBroker()
	b.failTimeout = 2
	s := New(b)
	o1, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	o2, _, _ := s.Place(ctx, "u1", "k2", limitBuy())
	b.filled["k1"] = big.NewRat(4, 1)
	b.filled["k2"] = big.NewRat(10, 1)
	s.Reconcile(ctx)
	if o1.Status != domain.PartiallyFilled || o2.Status != domain.Filled {
		t.Fatalf("got %s / %s", o1.Status, o2.Status)
	}
}

func TestReconcile_NotFoundStaysUnconfirmed(t *testing.T) {
	b := newBroker()
	s := New(b)
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	// имитируем: заявка ушла в UNCONFIRMED, а у брокера её уже нет среди активных
	o.Status = domain.Unconfirmed
	delete(b.posted, "k1")
	s.Reconcile(ctx)
	if o.Status != domain.Unconfirmed {
		t.Fatalf("должна остаться UNCONFIRMED до ручного разбора, got %s", o.Status)
	}
}

func TestPlaceOrder_BrokerRejected(t *testing.T) {
	b := newBroker()
	b.rejectWith = "insufficient funds"
	s := New(b)
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	if o.Status != domain.Rejected || o.Reason == "" {
		t.Fatalf("got %+v", o)
	}
	if len(s.Notifications()) != 1 {
		t.Fatal("отказ должен породить уведомление")
	}
}

func TestValidate_QuantityAndFields(t *testing.T) {
	for _, q := range []string{"0", "-1", "abc", ""} {
		p := limitBuy()
		p.Quantity = q
		if code(Validate(p)) != "VALIDATION_FAILED" {
			t.Errorf("quantity %q должна быть отклонена", q)
		}
	}
	p := limitBuy()
	p.Direction = "HOLD"
	if code(Validate(p)) != "VALIDATION_FAILED" {
		t.Error("направление HOLD должно быть отклонено")
	}
	p = limitBuy()
	p.OrderType = "MARKET" // с ценой
	if code(Validate(p)) != "VALIDATION_FAILED" {
		t.Error("цена у MARKET должна быть отклонена")
	}
}

func TestValidate_NegativePrice(t *testing.T) {
	p := limitBuy()
	p.Price = "-1"
	if code(Validate(p)) != "NEGATIVE_PRICE_NOT_ALLOWED" {
		t.Fatal("отрицательная цена не по фьючерсу должна быть отклонена")
	}
	p.IsFuture = true
	if err := Validate(p); err != nil {
		t.Fatalf("для фьючерса допустимо: %v", err)
	}
}

func TestValidate_LargeOrderRejected(t *testing.T) {
	p := limitBuy()
	p.Quantity = "200000"
	p.Price = "151" // 30 200 000
	if code(Validate(p)) != "ORDER_REQUIRES_CONFIRMATION" {
		t.Fatal("заявка выше 30 млн должна быть отклонена до брокера")
	}
	p.Price = "150" // ровно 30 000 000 — не выше порога
	if err := Validate(p); err != nil {
		t.Fatalf("ровно порог допустим: %v", err)
	}
}

func TestApplyFill_PartialThenFull(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.ApplyFill(o.ID, "t1", "4", "")
	if o.Status != domain.PartiallyFilled {
		t.Fatalf("got %s", o.Status)
	}
	s.ApplyFill(o.ID, "t2", "6", "")
	if o.Status != domain.Filled {
		t.Fatalf("got %s", o.Status)
	}
}

func TestApplyFill_DuplicateTradeIgnored(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.ApplyFill(o.ID, "t1", "4", "")
	s.ApplyFill(o.ID, "t1", "4", "") // повторная доставка
	if o.Filled.Cmp(big.NewRat(4, 1)) != 0 {
		t.Fatalf("filled=%s, объём не должен удваиваться", o.Filled.FloatString(1))
	}
}

func TestApplyFill_AfterTerminalIgnored(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.ApplyFill(o.ID, "t1", "10", "")
	s.ApplyFill(o.ID, "t2", "1", "")
	if o.Status != domain.Filled || o.Filled.Cmp(big.NewRat(10, 1)) != 0 {
		t.Fatal("после терминального состояния события игнорируются")
	}
}

func TestApplyFill_FromUnconfirmedDirectlyFilled(t *testing.T) {
	b := newBroker()
	b.failTimeout = 1
	s := New(b)
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.ApplyFill(o.ID, "t1", "10", "") // правило 3: события не по порядку
	if o.Status != domain.Filled {
		t.Fatalf("got %s", o.Status)
	}
}

func TestCancel_Flow(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	if _, err := s.Cancel(o.ID); err != nil || o.Status != domain.CancelRequested {
		t.Fatalf("got %s %v", o.Status, err)
	}
	if _, err := s.Cancel(o.ID); err != nil {
		t.Fatal("повтор отмены идемпотентен")
	}
	s.ConfirmCancel(o.ID)
	if o.Status != domain.Cancelled {
		t.Fatalf("got %s", o.Status)
	}
	if _, err := s.Cancel(o.ID); code(err) != "ORDER_NOT_CANCELLABLE" {
		t.Fatalf("got %v", err)
	}
}

func TestCancel_FillWinsOverCancel(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.Cancel(o.ID)
	s.ApplyFill(o.ID, "t1", "10", "")
	s.ConfirmCancel(o.ID) // запоздавшее подтверждение отмены
	if o.Status != domain.Filled {
		t.Fatalf("исполнение сильнее отмены, got %s", o.Status)
	}
}

func TestGet_ForeignOrderLooksMissing(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	if _, err := s.Get("u2", o.ID); code(err) != "ORDER_NOT_FOUND" {
		t.Fatalf("got %v", err)
	}
}

func TestNotifications_OnTerminal(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.ApplyFill(o.ID, "t1", "10", "")
	n := s.Notifications()
	if len(n) != 1 || n[0].Kind != "FILLED" {
		t.Fatalf("got %+v", n)
	}
}

func TestNotifications_Dedup(t *testing.T) {
	s := New(newBroker())
	o, _, _ := s.Place(ctx, "u1", "k1", limitBuy())
	s.ApplyFill(o.ID, "t1", "10", "")
	s.ApplyFill(o.ID, "t1", "10", "") // дубликат
	s.ApplyFill(o.ID, "t2", "1", "")  // после терминального
	if len(s.Notifications()) != 1 {
		t.Fatalf("ожидаем одно уведомление, got %d", len(s.Notifications()))
	}
}
