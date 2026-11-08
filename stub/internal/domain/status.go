// Package domain содержит модель состояний заявки OrderGuard
// (см. docs/03-order-state-machine.md).
package domain

type Status string

const (
	Created         Status = "CREATED"
	Sending         Status = "SENDING"
	Unconfirmed     Status = "UNCONFIRMED"
	New             Status = "NEW"
	PartiallyFilled Status = "PARTIALLY_FILLED"
	CancelRequested Status = "CANCEL_REQUESTED"
	Filled          Status = "FILLED"
	Cancelled       Status = "CANCELLED"
	Rejected        Status = "REJECTED"
)

// IsTerminal сообщает, что состояние окончательное и дальше не меняется (правило 1).
func (s Status) IsTerminal() bool {
	return s == Filled || s == Cancelled || s == Rejected
}

// allowed описывает допустимые переходы. Переходы в FILLED из SENDING и UNCONFIRMED
// разрешены напрямую: события могут приходить не по порядку (правило 3).
var allowed = map[Status][]Status{
	Created:         {Sending, Rejected},
	Sending:         {New, Rejected, Unconfirmed, Filled, PartiallyFilled},
	Unconfirmed:     {New, PartiallyFilled, Filled, Rejected},
	New:             {PartiallyFilled, Filled, CancelRequested, Rejected},
	PartiallyFilled: {PartiallyFilled, Filled, CancelRequested},
	CancelRequested: {Cancelled, Filled, PartiallyFilled},
}

// CanTransition проверяет, допустим ли переход from -> to.
func CanTransition(from, to Status) bool {
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}
