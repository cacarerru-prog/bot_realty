# FlatRadar — Telegram-бот мониторинга объявлений о продаже квартир

## 1. Цель проекта

Бот на Go, который **периодически опрашивает площадки объявлений** и, как только
появляется **новое объявление о продаже квартиры в Минске**, подходящее под мои
фильтры (в первую очередь по цене), **мгновенно присылает уведомление в Telegram**.

Главная ценность — скорость: увидеть свежий лот раньше остальных покупателей.

## 2. Площадки и способ получения данных

| Площадка   | Источник данных                              | Авторизация | Этап |
|------------|----------------------------------------------|-------------|------|
| Onliner.by | Публичный JSON API `r.onliner.by/sdapi/...`  | Не требуется | MVP  |
| Kufar.by   | Публичный JSON API `api.kufar.by/search-api/`| Не требуется | MVP  |
| Realt.by   | Парсинг HTML страницы поиска (b2bapi закрыт) | —           | Этап 2 |

Решение: на старте используем **официальные/публичные JSON-эндпоинты Onliner и
Kufar** (стабильно, без ключей), Realt.by добавляем позже парсингом.

## 3. Фильтры объявлений

- **Город:** всегда Минск (зашит в конфиг).
- **Цена:** диапазон от / до (главный фильтр) — в USD.
- Опционально (на будущее): количество комнат, площадь, этаж, район.

## 4. Логика работы (ядро)

1. **Планировщик** раз в N секунд (по умолчанию 60) запускает опрос всех площадок.
2. Для каждой площадки — **коллектор**: делает запрос с фильтрами, получает список
   объявлений, сортированных по свежести.
3. Каждое объявление приводится к единой модели `Listing` (id, площадка, цена,
   адрес, комнаты, площадь, ссылка, фото, время).
4. **Дедупликация:** id уже виденных объявлений хранятся в БД. Новые id → новые лоты.
5. Новое объявление под фильтр → **формируется сообщение** и отправляется в Telegram.
6. id помечается как «отправлено».

### Единая модель объявления
```
Listing {
  Source     string   // "onliner" | "kufar" | "realt"
  ExternalID string   // id на площадке
  Title      string
  Price      int      // USD
  Rooms      int
  Area       float64  // м²
  Floor      string
  Address    string
  URL        string
  Photo      string
  CreatedAt  time.Time
}
```

## 5. Архитектура (пакеты Go)

```
D:\bot
├── cmd/bot/main.go          // точка входа: конфиг → коллекторы → планировщик + слушатель команд
├── internal/
│   ├── config/              // загрузка config.json + дефолты
│   ├── model/               // Listing и общие типы
│   ├── collector/           // интерфейс Collector + реализации
│   │   ├── onliner.go       // ✅ публичный JSON API
│   │   ├── kufar.go         // ✅ публичный JSON API
│   │   └── realt.go         // этап 3 (парсинг HTML)
│   ├── storage/             // дедуп виденных id (seen.json)
│   ├── filter/              // проверка объявления под критерии
│   ├── state/              // потокобезопасное состояние (пауза, цена, последние)
│   ├── telegram/            // отправка объявлений + приём команд (long polling)
│   └── scheduler/           // тикер опроса площадок
├── deploy/flatradar.service // systemd-юнит для сервера
├── Dockerfile               // сборка контейнера (статический бинарник)
├── config.example.json      // шаблон настроек
├── config.json              // реальные настройки (в .gitignore)
├── go.mod
└── README.md
```

**Интерфейс коллектора** (легко добавлять новые площадки):
```go
type Collector interface {
    Name() string
    Fetch(ctx context.Context, f Filter) ([]Listing, error)
}
```

## 6. Хранилище

- На текущем этапе — **JSON-файл `seen.json`** со списком ключей виденных
  объявлений (`source:external_id`). Без зависимостей, переживает перезапуск.
- Апгрейд на будущее: SQLite (`modernc.org/sqlite`, без CGO) при росте объёма.

## 7. Доставка в Telegram и управление

- **Raw Telegram Bot API** через `net/http` — без внешних зависимостей.
- Бот создаётся через **@BotFather**, токен и `chat_id` кладутся в `config.json`.
- Отправка объявлений + приём команд (long polling `getUpdates`) в пакете
  `internal/telegram`. Команды принимаются только из своего `chat_id`.
- Формат сообщения: цена, комнаты/площадь, адрес, площадка, ссылка (превью).

Пример:
```
🏠 Новое объявление (Onliner)
💵 65 000 $ · 2 комн · 48 м² · 5/9 эт
📍 Минск, ул. Притыцкого
🔗 https://r.onliner.by/ak/apartments/...
```

## 8. Конфигурация (config.yaml / env)

```yaml
telegram_token: "..."        # из @BotFather
chat_id: 123456789
poll_interval: 60s
city: "Минск"
price_min: 0
price_max: 80000
sources: [onliner, kufar]
```

## 9. Технологии

- **Go 1.25**, только стандартная библиотека (ноль внешних зависимостей)
- `net/http` — запросы к API площадок и Telegram Bot API
- `encoding/json` — конфиг, хранилище, разбор ответов
- `goquery` — добавим для парсинга Realt на этапе 3

## 10. Этапы реализации

**Этап 1 — MVP (готово ✅):**
1. ✅ Каркас проекта, go.mod, конфиг.
2. ✅ Коллектор Onliner (JSON API) + модель Listing.
3. ✅ Хранилище seen-id (seen.json).
4. ✅ Фильтр по цене + город Минск.
5. ✅ Отправка в Telegram.
6. ✅ Планировщик-тикер + warm-up.

**Этап 2 — полноценный бот (готово ✅):**
7. ✅ Коллектор Kufar (JSON API).
8. ✅ Управление из чата: /status, /pause, /resume, /price, /last, /help.
9. ✅ Динамическая смена цены и пауза на лету (пакет state).
10. ✅ Деплой: Dockerfile + systemd, бинарник под Linux/ARM.

**Этап 3 (планы):**
11. Коллектор Realt.by (парсинг HTML).
12. Расширенные фильтры (комнаты, площадь, район).
13. SQLite вместо JSON при росте объёма.

## 11. Риски и нюансы

- Площадки могут менять API/вёрстку → коллекторы изолированы за интерфейсом,
  чинить можно по одному.
- Возможны лимиты на частоту запросов → разумный `poll_interval`, User-Agent,
  бэкофф при ошибках.
- Первый запуск: чтобы не получить лавину «новых» объявлений, при старте
  помечаем текущую выдачу как уже виденную (warm-up), уведомляем только о
  появившихся после старта.

## 12. Панель пользователя и команды

Управление — три способа: **кнопочная панель** (`/menu` или `/start`),
**меню «/»** рядом с полем ввода (регистрируется автоматически через
`setMyCommands`) и обычный ввод команд.

| Команда         | Кнопка панели    | Действие                              |
|-----------------|------------------|---------------------------------------|
| `/menu`         | —                | показать панель с кнопками            |
| `/status`       | 📊 Статус        | текущие настройки и статус            |
| `/sources`      | 🌐 Площадки      | подключённые площадки и их статус     |
| `/pause`        | ⏸ Пауза          | приостановить уведомления             |
| `/resume`       | ▶️ Возобновить   | возобновить уведомления               |
| `/price 60000`  | —                | изменить максимальную цену на лету    |
| `/last`         | 🆕 Последние     | последние найденные объявления        |
| `/help`         | ❓ Помощь        | список команд                         |

Команды принимаются только из своего `chat_id`.

## 13. Локальный запуск

```powershell
# 1. скопировать шаблон конфига и вписать токен + chat_id
copy config.example.json config.json
# 2. запустить
go run ./cmd/bot
# или собрать бинарник
go build -o flatradar.exe ./cmd/bot
.\flatradar.exe
```

## 14. Деплой на Oracle Cloud (Always Free, ARM, $0)

Бот делает только исходящие соединения — входящие порты открывать не нужно.

**1. Создать виртуалку.** В Oracle Cloud → Compute → Instances → Create:
- Shape: **VM.Standard.A1.Flex** (Ampere ARM, Always Free; хватит 1 OCPU / 6 ГБ).
- Image: **Ubuntu 22.04/24.04**.
- Добавить свой **SSH-ключ** (скачать приватный, если генерит Oracle).

**2. Собрать ARM-бинарник локально** (Windows PowerShell):
```powershell
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="arm64"
go build -o flatradar ./cmd/bot
```

**3. Залить бинарник и конфиг на сервер** (подставь IP):
```bash
ssh ubuntu@<IP> "sudo mkdir -p /opt/flatradar && sudo chown ubuntu /opt/flatradar"
scp flatradar config.json ubuntu@<IP>:/opt/flatradar/
```

**4. Настроить systemd-сервис** (на сервере по SSH):
```bash
sudo useradd -r -s /usr/sbin/nologin flatradar 2>/dev/null; sudo chown -R flatradar /opt/flatradar
sudo cp /opt/flatradar/flatradar.service /etc/systemd/system/   # или загрузить deploy/flatradar.service
sudo systemctl daemon-reload
sudo systemctl enable --now flatradar
sudo systemctl status flatradar          # проверить, что active (running)
journalctl -u flatradar -f               # смотреть логи
```

После этого бот работает круглосуточно, перезапускается сам при сбое
(`Restart=always`) и при перезагрузке сервера (`enable`).

**Обновление версии:** пересобрать бинарник, `scp` на сервер,
`sudo systemctl restart flatradar`.

> Альтернатива — через Docker: `docker build -t flatradar . && docker run -d
> --restart=always -v /opt/flatradar:/data --name flatradar flatradar`
> (config.json и seen.json лежат в `/opt/flatradar`).
