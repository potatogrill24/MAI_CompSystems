# Проверка реализации

Проверено локально 17 ноября 2026 года:

- `go test -race ./...` — успешно, включая миллион последовательностей и GraphQL → REST.
- `go vet ./...` — успешно.
- `go build -o bin/server ./cmd/server` — успешно.
- `go mod verify` — успешно.
- Dockerfile собирается, все 10 контейнеров Compose достигают `healthy`.
- `python3 scripts/smoke.py` — успешно: проекты, работы, циклы, миллион кандидатов, товары, атомарный резерв нескольких позиций, конкурентные заказы, RabbitMQ → журнал MongoDB, GraphQL, отмена и идемпотентный возврат товара.
- `python3 scripts/recovery.py` — успешно: подтверждённый заказ сохраняется при остановке RabbitMQ, уведомление доставляется после его запуска; заказ при остановке Inventory получает 202, после запуска склада подтверждается фоновым обработчиком.
- `go run github.com/getkin/kin-openapi/cmd/validate@v0.133.0 docs/openapi.json` — успешно.
- `go run github.com/yannh/kubeconform/cmd/kubeconform@v0.6.7 -strict -summary deploy/k8s` — все 26 ресурсов валидны.

Kubernetes проверен на соответствие схемам; развёртывание в работающем кластере в этом окружении не выполнялось.
