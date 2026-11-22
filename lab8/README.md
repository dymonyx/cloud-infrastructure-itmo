# Лабораторная работа №8. Elasticsearch и логи Chainwise

В этой лабораторной Elasticsearch используется для хранения и поиска логов
Chainwise. В тестовых данных есть сообщения от `frontend`, `bike-api`,
`maintenance-api`, `maintenance-worker`, `weather-integration`,
`notification-worker` и `postgresql`. Kibana нужна для запросов и визуализации,
а состояние Elasticsearch собирают `elasticsearch-exporter`, Prometheus и
Grafana.

## Что находится в папке

В `docker-compose.yml` описан весь стенд. Маппинг индекса лежит в
`config/index-template.json`, а политика ротации лежит в
`config/ilm-policy.json`. В `data` находятся 40 начальных логов и ещё 20 логов
для проверки rollover. Все запросы для Kibana собраны в
`kibana/dev-tools-queries.txt`.

Скрипт `setup-elasticsearch.sh` создаёт политику, шаблон, первый индекс и alias,
а затем загружает начальные данные. `run-queries.sh` выполняет четыре запроса из
задания через терминал. `trigger-rollover.sh` добавляет ещё 20 документов и
ждёт, пока ILM создаст следующий индекс.

## Подготовка

Для запуска нужны Docker Compose, `curl` и `jq`. Образы Elasticsearch и Kibana
занимают несколько гигабайт, поэтому сначала лучше проверить свободное место.

```bash
df -h /
docker system df
```

Если Docker раньше не запускал Elasticsearch, нужно проверить системный
параметр `vm.max_map_count`.

```bash
sysctl vm.max_map_count
```

Если значение меньше `262144`, его можно временно изменить такой командой.

```bash
sudo sysctl -w vm.max_map_count=262144
```

После этого нужно перейти в папку лабораторной и создать локальный `.env`.

```bash
cd /home/agonek/vscode/cloud-infrastructure-itmo/lab8
cp .env.example .env
```

## Часть 1. Запуск Elasticsearch и Kibana

Стенд запускается одной командой. При первом старте Docker сначала скачает
образы, поэтому запуск может занять несколько минут.

```bash
docker compose up -d --wait
docker compose ps
```

После запуска доступны следующие страницы.

| Сервис | Адрес |
| --- | --- |
| Elasticsearch | http://localhost:9200 |
| Kibana | http://localhost:5601 |
| Prometheus | http://localhost:9090 |
| Grafana | http://localhost:3000 |

Теперь нужно создать политику, шаблон, первый индекс и загрузить 40 логов.

```bash
./scripts/setup-elasticsearch.sh
```

Когда скрипт завершится без ошибок, нужно открыть Kibana, перейти в
`Management`, затем в `Dev Tools`, вставить запрос и нажать кнопку запуска.

```http
GET _cluster/health
```

Статус будет `yellow`, потому что у индекса настроены один primary shard и одна
реплика, а в стенде работает только один узел. Elasticsearch не размещает
primary shard и его реплику на одном узле, поэтому реплика остаётся
нераспределённой. Для `green` нужен второй узел, на который можно перенести
реплику. В локальном стенде статус также можно сделать зелёным, если установить
`number_of_replicas` равным нулю, но тогда копии данных не будет.

## Часть 2. Поиск и агрегация логов

В шаблоне индекса используется следующий маппинг.

| Поле | Тип |
| --- | --- |
| `timestamp` | `date` |
| `level` | `keyword` |
| `service` | `keyword` |
| `message` | `text` |
| `trace_id` | `keyword` |

Все запросы ниже уже есть в `kibana/dev-tools-queries.txt`, поэтому их можно
копировать оттуда по одному.

### Полнотекстовый поиск

В Dev Tools нужно выполнить запрос по слову `цепи`.

```http
GET chainwise-logs/_search
{
  "size": 20,
  "query": {
    "match": {
      "message": "цепи"
    }
  },
  "sort": [
    {"timestamp": "asc"}
  ]
}
```

### Ошибки за выбранный промежуток

Следующий запрос оставляет только записи уровня `ERROR` за один час.

```http
GET chainwise-logs/_search
{
  "size": 20,
  "query": {
    "bool": {
      "filter": [
        {"term": {"level": "ERROR"}},
        {
          "range": {
            "timestamp": {
              "gte": "2026-11-22T10:00:00Z",
              "lte": "2026-11-22T10:59:59Z"
            }
          }
        }
      ]
    }
  },
  "sort": [
    {"timestamp": "asc"}
  ]
}
```

### Поиск одной операции по trace_id

Для проверки цепочки одной поездки используется `trace-ride-1007`.

```http
GET chainwise-logs/_search
{
  "size": 20,
  "query": {
    "term": {
      "trace_id": "trace-ride-1007"
    }
  },
  "sort": [
    {"timestamp": "asc"}
  ]
}
```

### Агрегация по уровню

```http
GET chainwise-logs/_search
{
  "size": 0,
  "aggs": {
    "logs_by_level": {
      "terms": {
        "field": "level",
        "size": 10
      }
    }
  }
}
```

### Визуализация в Kibana

Сначала в `Stack Management` нужно открыть `Data Views` и создать представление
с именем `Chainwise logs`. В качестве шаблона используется
`chainwise-logs-*`, а полем времени выбирается `timestamp`.

Затем нужно открыть `Analytics`, перейти в `Visualize Library`, создать
визуализацию `Lens` и выбрать `Chainwise logs`. По горизонтали используется
`Top values of level`, а по вертикали `Count of records`. Тип графика можно
оставить столбчатым. Если Kibana не показывает документы из-за диапазона
времени, в правом верхнем углу нужно выбрать весь день 22 ноября 2026 года.

При необходимости все четыре запроса можно дополнительно проверить в
терминале.

```bash
./scripts/run-queries.sh
```

## Часть 3. ILM и автоматический rollover

Политика `chainwise-logs-policy` переводит запись в новый индекс, когда primary
shard достигает 1 МБ, когда в индексе появляется 50 документов или когда ему
исполняется один день. Старый индекс удаляется через один день после rollover.
Маленькие значения нужны только для быстрой проверки механизма на локальном
стенде.

Шаблон связывает политику с индексами `chainwise-logs-*`, а запись идёт через
alias `chainwise-logs`. За счёт этого отправителю логов не нужно знать имя
текущего индекса.

Политику можно посмотреть в Kibana через `Stack Management`, затем
`Index Lifecycle Policies` и `chainwise-logs-policy`.

После этого в терминале нужно выполнить скрипт.

```bash
./scripts/trigger-rollover.sh
```

Скрипт добавит ещё 20 документов через alias. Общее число документов превысит
порог 50, а ILM автоматически создаст `chainwise-logs-000002` и сделает его
новым write index.

Если новый индекс не появился за две минуты, состояние можно проверить в Kibana.

```http
GET chainwise-logs-*/_ilm/explain
GET _cat/indices/chainwise-logs-*?v&s=index
GET _cat/aliases/chainwise-logs?v
```

## Часть 4. Мониторинг и алерты

После запуска нужно подождать около 30 секунд, открыть Grafana по адресу
http://localhost:3000 и войти с логином `admin` и паролем `admin`. Дашборд
`Chainwise Elasticsearch` создаётся автоматически.

На нём находятся состояние кластера, число нераспределённых shards,
использование JVM heap, свободное место, число документов и размер индексов.

Для алертов выбраны именно эти три показателя.

| Alert | Условие | Что он ловит и чем это грозит |
| --- | --- | --- |
| `ChainwiseElasticsearchClusterRed` | Состояние кластера остаётся `red` 30 секунд | В кластере не назначен хотя бы один primary shard, поэтому часть логов нельзя записать или найти |
| `ChainwiseElasticsearchHeapHigh` | JVM heap выше 80 процентов две минуты | Elasticsearch тратит всё больше времени на сборку мусора, запросы замедляются, а при дальнейшем росте процесс может завершиться из-за нехватки памяти |
| `ChainwiseElasticsearchDiskLow` | Свободного места меньше 1 ГиБ одну минуту | Новые shards не смогут нормально размещаться, а индекс может перейти в режим блокировки записи, из-за чего свежие логи перестанут сохраняться |

Правила уже загружены в Prometheus из
`monitoring/prometheus/alerts.yml`. Их можно проверить на странице
http://localhost:9090/alerts или командой.

```bash
./scripts/check-monitoring.sh
```

В обычном состоянии правила находятся в состоянии `inactive`, потому что их
условия сейчас не выполняются.

## Остановка стенда

Контейнеры можно остановить без удаления данных.

```bash
docker compose stop
```

Для следующего запуска достаточно выполнить `docker compose up -d --wait`.
Если лабораторные данные больше не нужны, контейнеры и связанные с этим стендом
volumes можно удалить командой ниже. После неё индексы, история Prometheus и
настройки Grafana восстановить уже не получится.

```bash
docker compose down -v
```
