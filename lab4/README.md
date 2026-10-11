# Лабораторная работа №4. Kafka для Chainwise

Между `bike-api` и `maintenance-worker` добавлена Kafka в режиме KRaft.
`bike-api` публикует событие `trip-recorded`, а worker читает его в consumer
group `maintenance-workers` и рассчитывает необходимость обслуживания
велосипеда.

## Запуск

```bash
cd lab4
docker compose up -d --build
docker compose ps
```

Kafka, топик, сервисы, Kafka UI и мониторинг описаны в `docker-compose.yml`.
Топик создаёт `topic-controller` через Kafka Admin API с параметрами из `.env`.

| Компонент | Адрес |
| --- | --- |
| `bike-api` | <http://localhost:8081> |
| `maintenance-worker-1` | <http://localhost:8082> |
| `maintenance-worker-2` | <http://localhost:8083> |
| `maintenance-worker-3` | <http://localhost:8084> |
| `maintenance-worker-4` | <http://localhost:8085> |
| Kafka UI | <http://localhost:8080> |
| Prometheus | <http://localhost:9090> |
| Grafana | <http://localhost:3000> |

В базовой конфигурации топик `trip-recorded` имеет три партиции и retention
172800000 мс. Поездку можно создать на странице `bike-api`.
Полученное событие появится на странице worker-а вместе с partition и offset.

## Проверенные сценарии

| Сценарий | Изменение | Результат |
| --- | --- | --- |
| Второй worker | Запущен `maintenance-worker-2` из профиля `scale` в той же consumer group | После rebalance партиции и события распределились между двумя экземплярами |
| Consumer-ов больше, чем партиций | Запущены четыре worker-а при трёх партициях | Три worker-а получили по одной партиции, четвёртый остался без назначения |
| Остановка worker-а | `maintenance-worker-1` остановлен, во время простоя созданы новые поездки | Сообщения сохранились в Kafka, lag вырос, после запуска worker обработал накопившиеся события |
| Увеличение потока | В `.env` значение `KAFKA_PARTITIONS` изменено с 3 на 6, затем повторно запущен `topic-controller` | Появилось шесть слотов обработки, ранее пустой worker получил partition 3 |
| Возврат к трём партициям | В конфигурацию возвращено значение 3 | Контроллер вывел предупреждение, фактически осталось шесть партиций. Kafka не уменьшает их число, поскольку это нарушило бы распределение ключей и порядок сообщений |
| Поиск поездки | Событие найдено по `trip_id` во вкладке Messages в Kafka UI | Определены partition и offset, а по позиции группы и lag подтверждена обработка сообщения |
| Повторная обработка истории | Через Kafka UI offsets всех партиций сброшены в `earliest` | После запуска worker на его странице снова появились старые события. Kafka хранит журнал отдельно от позиции группы, поэтому данные можно перечитать |
| Удаление старых сообщений | Временно заданы `KAFKA_RETENTION_MS=15000` и `KAFKA_SEGMENT_MS=5000` | После истечения retention прочитанные сообщения исчезли из Kafka UI. Удаление определяется сроком хранения, а не фактом чтения |

Дополнительные worker-ы запускаются так:

```bash
docker compose --profile scale --profile overflow up -d \
  maintenance-worker-2 maintenance-worker-3 maintenance-worker-4
```

Изменения параметров применяются так:

```bash
docker compose up --no-deps --force-recreate topic-controller
docker compose logs topic-controller
```

Сброс offsets выполнялся через **Kafka UI → Consumers → maintenance-workers → Reset offsets →
Earliest**.

## Мониторинг

Prometheus получает метрики от `kafka_exporter`, `bike-api` и worker-ов. Дашборд
`Chainwise Kafka` содержит число broker-ов и партиций, участников группы, общий
lag и lag по партициям, текущие offsets и скорость обработки событий.

| Alert | Условие | Что обнаруживает |
| --- | --- | --- |
| `KafkaBrokerUnavailable` | Нет доступного broker-а 15 секунд | Producer не может записывать, а consumer читать события |
| `MaintenanceConsumerMissing` | В группе нет участников 30 секунд | События копятся, но расчёт обслуживания не выполняется |
| `KafkaConsumerLagHigh` | Суммарный lag больше 5 сообщений 30 секунд | Worker-ы остановлены или не успевают за потоком |

## Где находятся файлы

| Путь | Содержимое |
| --- | --- |
| `docker-compose.yml`, `.env` | Конфигурация стенда и топика |
| `bike-api/` | Producer и его Dockerfile |
| `maintenance-worker/` | Consumer и его Dockerfile |
| `topic-controller/` | Декларативное создание и изменение топика |
| `monitoring/` | Prometheus, alert rules и дашборд Grafana |
| `screenshots/` | 26 снимков с результатами проверок |
| `report.md` | Подробное описание работы и скриншоты |

## Проверка и остановка

```bash
go test ./...
docker compose --profile scale --profile overflow down
```
