# 10. Матрица трассируемости

Связывает требование с сценарием, диаграммой, операцией API, моделью данных и проверкой. Если в строке пусто, это сигнал, что чего-то не хватает.

| Требование | Сценарий | Диаграмма | Операция API | Данные | Проверка |
|---|---|---|---|---|---|
| ORD-1 | S-1 | [4.1](04-integration.md) | `POST /v1/orders` | `ORDER` | Приёмка ORD-1; тест `TestPlaceOrder_Accepted` |
| ORD-2 | S-2.1, S-2.2 | [4.1](04-integration.md) | `POST /v1/orders` (`Idempotency-Key`) | `ORDER.idempotency_key`, `params_hash` | `TestPlaceOrder_IdempotentRepeat`, `TestPlaceOrder_KeyReusedWithOtherParams` |
| ORD-3 | S-3.3 | [4.3](04-integration.md) | `DELETE /v1/orders/{id}` | `ORDER_EVENT` | `TestCancel_*` |
| ORD-4 | S-3.1 | [4.1](04-integration.md), [состояния](03-order-state-machine.md) | `GET /v1/orders/{id}` | `ORDER.status` | `TestPlaceOrder_TimeoutBecomesUnconfirmed`, `TestReconcile_ResolvesUnconfirmed` |
| ORD-5 | S-2.4 | не нарисована (см. ниже) | `POST /v1/orders/{id}/replace` | `ORDER_EVENT` | Не покрыто в заглушке |
| ORD-6 | S-3, негативные проверки | — | `POST /v1/orders` (422) | — | `TestValidate_*` |
| ORD-7 | S-3.5 | — | `POST /v1/orders` (422) | — | `TestValidate_LargeOrderRejected` |
| TRK-1 | S-1 | [4.2](04-integration.md) | — (внутреннее) | `FILL` | `TestApplyFill_*` |
| TRK-2 | S-3.2 | [4.2](04-integration.md) | `GET /v1/health` | `RECONCILIATION_FINDING` | `TestReconcile_*` |
| TRK-3 | S-3.2 | [4.2](04-integration.md) | — | `RECONCILIATION_FINDING` | Нагрузочная проверка (не в заглушке) |
| TRK-4 | S-3.2 | [4.2](04-integration.md) | `GET /v1/health` | — | Ручная проверка |
| NTF-1 | S-1 | [4.2](04-integration.md) | `GET /v1/notifications` | `NOTIFICATION` | `TestNotifications_OnTerminal` |
| NTF-2 | S-2.3 | [4.2](04-integration.md) | — | `NOTIFICATION.dedup_key` | Дедупликация: `TestNotifications_Dedup`. Окно агрегации 30 с в заглушке не реализовано |
| NTF-3 | S-3.2 | [4.2](04-integration.md) | `GET /v1/health` | — | Ручная проверка |
| NTF-4 | — | — | `PUT /v1/notification-settings` | `NOTIFICATION_SETTING` | Не в MVP |
| SEC-1 | S-3.6 | [4.4](04-integration.md) | все | `BROKER_CREDENTIAL.token_ref` | Ревью журналов |
| SEC-2 | S-3.6 | — | `PUT /v1/credentials` | `BROKER_CREDENTIAL.scope` | Тест прав |
| SEC-3 | — | — | — | `ORDER_EVENT` | Проверка журнала |
| OPS-1 | — | — | `GET /v1/orders/{id}/events` | `ORDER_EVENT` | Ручная проверка |
| OPS-2 | — | [4.4](04-integration.md) | — | `BROKER_CREDENTIAL.environment` | Конфигурационный тест |
| OPS-3 | S-3.4 | — | `429` в ответах | — | Тест ограничителя |

## Что показала матрица

- **ORD-5 (замена заявки)** не нарисована на диаграмме последовательности и не покрыта в заглушке. Это осознанный долг приоритета S, зафиксированный здесь, а не забытый.
- **NTF-2:** дедупликация повторов проверена, а склейка частичных исполнений в одно уведомление за 30 секунд в заглушке не реализована.
- **TRK-3, TRK-4, NTF-3** проверяются вручную или нагрузочным тестом, автоматических тестов на уровне заглушки нет.
- Не у каждого требования приоритета M есть автоматический тест: SEC-1, SEC-3, OPS-1, OPS-2, OPS-3 и TRK-3 проверяются ревью, конфигурацией или нагрузочным тестом. Отдельного сценария нет у SEC-3, OPS-1 и OPS-2: это сквозные требования, их стоит описать сценариями в следующей итерации.
- Остальные требования приоритета M (ORD-1, ORD-2, ORD-3, ORD-4, ORD-6, TRK-1, TRK-2, NTF-1) связаны со сценарием, операцией API и автотестом в заглушке.
