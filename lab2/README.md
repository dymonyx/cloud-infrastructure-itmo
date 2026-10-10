# Лабораторная работа №2 — наблюдаемость Chainwise

В работе поднят полный локальный стенд наблюдаемости для `bike-api` из
архитектуры Chainwise.

## Что входит в стенд

| Компонент | Назначение | Локальный адрес |
| --- | --- | --- |
| `bike-api` | Демонстрационный сервис Chainwise | <http://localhost:8081> |
| Prometheus | Сбор RED-метрик и вычисление alert rules | <http://localhost:9090> |
| Grafana | Общий интерфейс метрик и логов | <http://localhost:3000> |
| Loki | Хранилище логов | <http://localhost:3100> |
| Grafana Alloy | Сбор stdout Docker-контейнера и отправка в Loki | <http://localhost:12345> |
| Jaeger | Приём OTLP-трейсов и просмотр waterfall | <http://localhost:16686> |
| Alertmanager | Группировка и доставка алертов | <http://localhost:9093> |

Promtail заменён на Grafana Alloy как на его актуальный эквивалент. Alloy
обнаруживает Docker-контейнер `bike-api`, читает его stdout и отправляет записи в
Loki.

## Запуск

```bash
docker compose up -d --build
```

После запуска открыть <http://localhost:8081>. На странице находятся три
демонстрационные кнопки:

- **Cause an error** вызывает `GET /bike/demo/error`, возвращает HTTP 500 и
  помечает span как ошибочный;
- **Cause a delay** вызывает `GET /bike/demo/delay`, ждёт две секунды внутри
  вложенного span `slow-dependency`;
- **Load** вызывает `GET /bike/demo/load?count=120`, который создаёт 120
  параллельных обращений к `/bike/profile`.

Проверить состояние контейнеров:

```bash
docker compose ps
```

Остановить стенд:

```bash
docker compose down
```

## Метрики и RED-дашборд

Сервис публикует `/metrics` в формате Prometheus:

- `chainwise_http_requests_total` — число запросов с метками метода, пути и
  HTTP-статуса;
- `chainwise_http_errors_total` — число ответов 5xx;
- `chainwise_http_request_duration_seconds` — гистограмма времени ответа.

Эти метрики образуют RED-модель:

- **Rate** показывает интенсивность запросов и всплески нагрузки;
- **Errors** показывает долю неуспешных запросов, а не только абсолютное число;
- **Duration** представлена p95, чтобы видеть задержку большинства медленных
  запросов и не скрывать её средним значением.

Дашборд `Chainwise RED` создаётся автоматически из
[`monitoring/grafana/dashboards/chainwise-red.json`](monitoring/grafana/dashboards/chainwise-red.json).

![RED dashboard](screenshots/grafana-red-dashboard.jpg)

## Логи и связь с trace

Все логи `bike-api` записываются в stdout в JSON и содержат `level`, `msg`,
`service` и `trace_id`. В Grafana настроен Loki derived field `TraceID`: значение
`trace_id` становится ссылкой на соответствующий trace в Jaeger.

Запрос панели логов:

```logql
{service_name="bike-api"} | json
```

![Ошибка в логах Grafana](screenshots/grafana-error-log.jpg)

## Трейсы

HTTP-запросы автоматически инструментированы OpenTelemetry, а экспорт выполняется
по OTLP/HTTP на `http://jaeger:4318`. Обработчик задержки создаёт вложенный span,
а обработчик ошибки записывает exception и устанавливает status `Error`.

![Delay trace](screenshots/jaeger-delay-trace.jpg)

![Error trace](screenshots/jaeger-error-trace.jpg)

## Алерты

Правила находятся в
[`monitoring/prometheus/alerts.yml`](monitoring/prometheus/alerts.yml).

| Alert | Условие | Что обнаруживает и чем опасно |
| --- | --- | --- |
| `ChainwiseHighErrorRate` | Доля 5xx больше 20% за 5 минут, состояние сохраняется 10 секунд | Массовые ошибки приложения или зависимости; пользователи не могут выполнить сценарий обслуживания велосипеда |
| `ChainwiseRequestSpike` | Средний RPS больше 0,3 за 5 минут, состояние сохраняется 5 секунд | Необычный всплеск запросов; угрожает исчерпанием CPU, памяти и лимитов зависимостей |
| `ChainwiseHighP95Latency` | p95 `/bike/demo/delay` больше 1 секунды за 5 минут, состояние сохраняется 5 секунд | Деградацию времени ответа; даже успешные запросы становятся неприемлемо медленными |

Alertmanager отправляет уведомления на webhook
`POST /internal/alerts`.

![Firing alerts](screenshots/alertmanager-firing.jpg)

## Проверка

Код собирается и проверяется командой:

```bash
go test ./...
```