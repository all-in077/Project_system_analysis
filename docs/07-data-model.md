# 07. Модель данных

## ER-диаграмма

```mermaid
erDiagram
    USER ||--o{ BROKER_CREDENTIAL : владеет
    USER ||--o{ ORDER : создаёт
    USER ||--o{ NOTIFICATION_SETTING : настраивает
    ORDER ||--o{ ORDER_EVENT : "имеет историю"
    ORDER ||--o{ FILL : "исполняется сделками"
    ORDER ||--o{ NOTIFICATION : порождает
    ORDER ||--o{ RECONCILIATION_FINDING : "затрагивается расхождением"

    USER {
        uuid id PK
        string email
        timestamp created_at
    }
    BROKER_CREDENTIAL {
        uuid id PK
        uuid user_id FK
        string environment "sandbox | production"
        string token_ref "ссылка на секрет, не сам токен"
        string scope "read_only | trade"
        timestamp created_at
    }
    ORDER {
        uuid id PK
        uuid user_id FK
        uuid idempotency_key "уникален вместе с user_id"
        string params_hash "для проверки повтора с другими параметрами"
        string broker_order_id "биржевой идентификатор, пока неизвестен - пусто"
        string instrument_id
        string direction "buy | sell"
        string order_type "limit | market"
        decimal quantity_lots
        decimal price
        decimal filled_lots
        decimal avg_price
        string status
        string reject_reason
        timestamp created_at
        timestamp updated_at
    }
    ORDER_EVENT {
        uuid id PK
        uuid order_id FK
        string from_status
        string to_status
        string source "user | stream | reconcile | system"
        string payload_ref
        timestamp occurred_at
    }
    FILL {
        uuid id PK
        uuid order_id FK
        string broker_trade_id "ключ дедупликации"
        decimal lots
        decimal price
        timestamp executed_at
    }
    NOTIFICATION {
        uuid id PK
        uuid order_id FK
        string kind
        string dedup_key
        string channel
        string status "queued | sent | failed"
        timestamp created_at
    }
    NOTIFICATION_SETTING {
        uuid id PK
        uuid user_id FK
        string channel
        boolean enabled
        string quiet_hours
    }
    RECONCILIATION_FINDING {
        uuid id PK
        uuid order_id FK
        string kind "missing_at_broker | status_mismatch | late_fill"
        string resolution
        timestamp found_at
    }
```

## Словарь ключевых полей

| Поле | Правило |
|---|---|
| `ORDER.idempotency_key` | UUID от клиента; уникальный индекс `(user_id, idempotency_key)`; хранится дольше, чем срок отслеживания у брокера (1 месяц) |
| `ORDER.params_hash` | Хеш параметров команды; при повторе с тем же ключом сравнивается, различие даёт `409` |
| `ORDER.broker_order_id` | Заполняется только после подтверждения; для `UNCONFIRMED` пусто |
| `ORDER.status` | Одно из состояний из [03](03-order-state-machine.md) |
| `ORDER.filled_lots` | Накопленный объём; только растёт |
| `FILL.broker_trade_id` | Уникален в рамках заявки; дедупликация повторной доставки |
| `NOTIFICATION.dedup_key` | `(order_id, вид события, накопленный объём)`; одно уведомление на событие |
| `BROKER_CREDENTIAL.token_ref` | Ссылка на запись в хранилище секретов; сам токен в базе не лежит |

## Правила целостности

1. Пара `(user_id, idempotency_key)` уникальна.
2. Изменение `ORDER.status` и запись в `ORDER_EVENT` происходят в одной транзакции.
3. `filled_lots` равно сумме `FILL.lots` по заявке.
4. `FILL.broker_trade_id` уникален внутри заявки.
5. `BROKER_CREDENTIAL` с `scope = read_only` не может использоваться для торговых команд.

## Хранение и удаление

| Данные | Срок | Основание |
|---|---|---|
| Заявки, события, сделки | не менее 5 лет (допущение) | Аудит и разбор споров |
| Токен брокера | До отзыва пользователем | Минимизация: удаляется по запросу и при удалении аккаунта |
| Журналы запросов | 90 дней, без токенов и персональных данных | Диагностика |
