# MAI CompSystems

Учебный проект на Go: REST API управления проектами и расчёта расписания, четыре сервиса из общей схемы, GraphQL и контейнерное развёртывание.

## Запуск

Требуются Docker с Compose v2. Для разработки без контейнеров — Go 1.26; для сквозной проверки — Python 3.

```bash
cd MAI_CompSystems
docker compose up --build -d --wait
python3 scripts/smoke.py
```

Первый запуск скачивает образы и Go-зависимости. `docker compose ps` показывает готовность сервисов, `docker compose logs -f order notification` — обработку заказов и сообщений. Остановка с сохранением данных: `docker compose down`. Тома PostgreSQL, MongoDB и RabbitMQ сохраняются между запусками.

| Сервис | Адрес | Данные / назначение |
|---|---|---|
| GraphQL | http://localhost:8080/graphql | Единая схема Product + Order |
| Management | http://localhost:8081 | Проекты и работы, PostgreSQL |
| Calculator | http://localhost:8082 | Параллельный расчёт расписаний |
| Product | http://localhost:8083 | Товары, PostgreSQL |
| Inventory | http://localhost:8084 | Склад и резервы, PostgreSQL |
| Order | http://localhost:8085 | Заказы с позициями, MongoDB |
| Notification | http://localhost:8086 | Журнал сообщений, MongoDB |

Все сервисы предоставляют `GET /healthz` и `GET /readyz`. В Compose HTTP-порты доступны только на localhost; базы и брокер доступны внутри сети Compose. Учебные учётные данные: `mai / mai`.

## Соответствие заданиям

| Задание | Реализация |
|---|---|
| Management из сообщения | CRUD проектов, добавление/удаление работ, изменение предшественников |
| Вычислительный сервис | Отдельный процесс Calculator, 1 000 000 случайных последовательностей, пул горутин, ресурс ≤ 10 |
| PDF, лабораторная 2 | Product / Inventory в PostgreSQL, Order / Notification в MongoDB; поля по таблицам PDF |
| PDF, лабораторная 3 | Order синхронно резервирует Inventory по REST, асинхронно сообщает Notification через RabbitMQ |
| PDF, лабораторная 4 | GraphQL queries и mutations поверх REST Product / Order, вложенное поле `OrderItem.product` |
| PDF, лабораторная 5 | Общий Dockerfile, Compose, Kubernetes Deployments / Services / PVC / ConfigMap / Secret |

```mermaid
flowchart LR
    Client --> Management
    Management --> Calculator
    Management --> PostgreSQL
    Client --> GraphQL
    GraphQL --> Product
    GraphQL --> Order
    Product --> PostgreSQL
    Order --> MongoDB
    Order -->|REST: резерв| Inventory
    Inventory --> PostgreSQL
    Order -->|outbox + RabbitMQ| Notification
    Notification --> MongoDB
```

Для компактного локального стенда три сервиса используют один экземпляр PostgreSQL с отдельными таблицами. Заказы и уведомления используют отдельные базы MongoDB. Один исполняемый файл запускается в семи контейнерах с различным `SERVICE`.

## Management и расчёт

В терминах первой задачи «задача» верхнего уровня называется проектом (`/projects`); вложенная работа — `work`.

```bash
curl -s localhost:8081/projects -H 'Content-Type: application/json' \
  -d '{"order_name":"изготовить изделие","start_date":"01-01-2022"}'

# Подставьте id созданного проекта вместо 1.
curl -s localhost:8081/projects/1/works -H 'Content-Type: application/json' \
  -d '{"task":"задача 1","duration":2,"resource":6}'
curl -s localhost:8081/projects/1/works -H 'Content-Type: application/json' \
  -d '{"task":"задача 2","duration":3,"resource":4}'
curl -s localhost:8081/projects/1/works -H 'Content-Type: application/json' \
  -d '{"task":"сборка","duration":4,"resource":10}'
curl -s -X PUT localhost:8081/projects/1/works/3/predecessors \
  -H 'Content-Type: application/json' -d '{"pred":[1,2]}'
curl -s localhost:8081/projects/1/calculate \
  -H 'Content-Type: application/json' -d '{"seed":42}'
```

Результат этого примера: `duration: 7`. Первые две работы выполняются одновременно, сборка начинается после завершения обеих. Ответ содержит длительность, число проверенных последовательностей, `seed`, количество работников, последовательность выбора и расписание `{work_id,start,finish,resource}`. Моменты времени отсчитываются от `start_date` в тех же условных единицах, что и `duration`; календарь выходных не задан.

По умолчанию вычисляются ровно **1 000 000** кандидатов. Для отладки можно передать `iterations` от 1 до 1 000 000 и `workers` от 1 до 64. При отсутствии `workers` используется `min(GOMAXPROCS,32)`. Один и тот же `seed` даёт одинаковое расписание независимо от числа работников. Значение по умолчанию — 0.

Алгоритм случайно выбирает следующую работу среди тех, чьи предшественники уже включены в расписание. Каждую выбранную работу вставляет в самый ранний допустимый интервал, проверяя суммарный ресурс на всём интервале. Из миллиона полученных расписаний выбирается самое короткое. Это случайный поиск, прообраз генетического алгоритма; глобальный оптимум не гарантирован, кандидаты могут повторяться. Предшественник должен **завершиться** до начала зависимой работы. Параллельные работы разрешены при суммарном ресурсе ≤ 10, прерывание работы не предусмотрено.

Лимиты: 100 работ в проекте, `duration` 1–1 000 000, `resource` 1–10. Расчёт ограничен 120 секундами и отменяется при отключении клиента; сложный проект может потребовать уменьшить `iterations`. Одновременно Calculator принимает один расчёт, остальные получают 429. Список предшественников заменяется целиком, `{"pred":[]}` очищает его. Циклы, ссылки на чужие или отсутствующие работы, повторы и ссылки на себя отклоняются. Перед удалением используемой работы нужно удалить ссылки на неё.

## Товары, склад и заказы

```bash
curl -s localhost:8083/products -H 'Content-Type: application/json' \
  -d '{"code":"P1","name":"Изделие","weight":1.5,"description":"Описание"}'
curl -s localhost:8084/inventory -H 'Content-Type: application/json' \
  -d '{"code":"P1","name":"Изделие","quantity":10,"cost":"125.50"}'
curl -s localhost:8085/orders -H 'Content-Type: application/json' \
  -d '{"code":"O1","date":"2026-11-17","items":[{"product_code":"P1","quantity":3}]}'
curl -s localhost:8086/notifications
```

Стоимость — десятичная строка с максимум двумя знаками после точки; в PostgreSQL это `NUMERIC(14,2)`. В позиции заказа хранится стоимость единицы. Имя и стоимость позиций берутся со склада при резервировании; переданные клиентом значения не задают цену. Количества целочисленные. Коды состоят из латинских букв, цифр, `_`, `-`, `.`, начинаются с буквы/цифры и имеют длину 1–64. Даты проектов и заказов: `DD-MM-YYYY` или `YYYY-MM-DD`; даты уведомлений: RFC3339.

Заказ сначала сохраняется в MongoDB как `pending`, затем синхронно резервирует все позиции на складе. Резервирование атомарно в PostgreSQL: если одной позиции не хватает, остатки всех остальных сохраняются. Повтор запроса с тем же кодом резерва не списывает товар повторно. При успехе заказ получает `confirmed`, а событие сохраняется **в том же документе** как outbox. При недостатке товара — `rejected` и HTTP 409. При временной недоступности Inventory — HTTP 202; фоновый обработчик продолжает запросы, состояние можно получить через `GET /orders/{code}`.

Outbox отправляется в устойчивую очередь RabbitMQ с persistent-сообщениями и подтверждениями публикации. При недоступности брокера заказ остаётся сохранённым, отправка повторяется. Notification подтверждает доставку после записи в MongoDB; ID события защищает от повторной регистрации. Доставка — at least once. Журнал регистрирует событие подтверждения заказа; отдельный канал отправки email/SMS в задании не указан.

Удаление заказа выполняет компенсацию: состояние `cancelling`, идемпотентный возврат товара, затем `cancelled`. При временной ошибке возвращается 202, завершение повторяет обработчик. Удалённый заказ скрыт из REST-списка, но его код сохраняется для защиты от повторного списания. Повторно использовать этот код нельзя. Событие создания остаётся историческим фактом. Редактировать можно дату активного заказа; для изменения позиций отмените его и создайте новый с другим кодом. `PUT /inventory/{code}` задаёт текущий доступный остаток, а не добавляет количество. Удаление складской записи с активным резервом возвращает 409.

## GraphQL

```bash
curl -s localhost:8080/graphql -H 'Content-Type: application/json' -d '{
  "query":"query($code:String!){order(code:$code){code date status items{quantity cost product{code name weight}}} products{code name}}",
  "variables":{"code":"O1"}
}'
```

Запросы: `products`, `product(code:)`, `orders`, `order(code:)`. Мутации: `createProduct(input:)`, `updateProduct(input:)`, `deleteProduct(code:)`, `createOrder(input:)`, `updateOrder(code:,date:)`, `deleteOrder(code:)`. Полная схема: [docs/schema.graphql](docs/schema.graphql). Отсутствующий или удалённый товар во вложенном поле даёт `null` и GraphQL-ошибку; сохранённое имя позиции остаётся доступным.

REST API и примеры моделей описаны в [docs/openapi.json](docs/openapi.json) (OpenAPI 3.0.3). Его можно импортировать в Swagger Editor или Postman. Размер JSON-запроса ограничен 1 MiB, неизвестные поля и несколько JSON-объектов отклоняются. Ошибки имеют форму `{"error":"..."}`; штатные коды: 400, 404, 409, 413, 429, 502, 503.

## Разработка и проверки

```bash
go test -race ./...
go vet ./...
go build -o bin/server ./cmd/server
# Пример запуска отдельного сервиса; БД должны быть доступны по указанным URL.
SERVICE=management DATABASE_URL='postgres://mai:mai@localhost:5432/mai?sslmode=disable' ./bin/server
```

Все переменные окружения перечислены в [.env.example](.env.example). По умолчанию каждый сервис использует порт из таблицы выше. `HTTP_ADDR` переопределяет адрес. При первом запуске сервисы создают отсутствующие таблицы, без удаления существующих данных. Это начальная схема учебного проекта; изменения схемы в дальнейшем нужно оформлять миграциями.

Модульные тесты проверяют расчёт расписаний, ограничения ресурса, зависимости, отмену, воспроизводимость и миллион кандидатов. `scripts/smoke.py` проверяет семь живых сервисов: CRUD, запрет циклов, расчёт, GraphQL, доставку RabbitMQ → MongoDB, конкурентные заказы и возврат остатков. Скрипт использует уникальные коды и удаляет созданные активные сущности; исторические заказы и сообщения сохраняются. `python3 scripts/recovery.py` дополнительно проверяет восстановление после временной остановки RabbitMQ и Inventory; он останавливает только эти сервисы данного Compose-проекта и запускает их обратно.

## Kubernetes

Манифесты находятся в [deploy/k8s](deploy/k8s). Требуются кластер, `kubectl`, StorageClass по умолчанию и доступный узлам образ `mai-compsystems:local`. Например, для существующего локального kind-кластера:

```bash
docker build -t mai-compsystems:local .
kind load docker-image mai-compsystems:local
kubectl apply -f deploy/k8s/00-config.yaml
kubectl apply -f deploy/k8s/10-infrastructure.yaml
kubectl apply -f deploy/k8s/20-applications.yaml
kubectl -n mai-compsystems wait --for=condition=Available deployment --all --timeout=300s
kubectl -n mai-compsystems port-forward service/graphql 8080:8080
```

В другом терминале можно пробросить `service/management 8081:8080` и аналогично остальные сервисы. Перед развёртыванием вне локального стенда замените учебные значения в Secret и задайте адрес собственного registry в Deployments. Инфраструктура использует PVC, приложения — readiness/liveness probes, ограничение ресурсов и запуск без root. Манифесты в JSON-синтаксисе YAML поддерживаются `kubectl` напрямую.

Документация используемых клиентов: [pgx](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool), [MongoDB Go Driver](https://pkg.go.dev/go.mongodb.org/mongo-driver/v2/mongo), [RabbitMQ AMQP](https://pkg.go.dev/github.com/rabbitmq/amqp091-go), [graphql-go](https://pkg.go.dev/github.com/graphql-go/graphql).
