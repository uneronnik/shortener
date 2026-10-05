# shortener

Сервис сокращения ссылок на Go: создаёт короткий код для URL, перенаправляет по нему и считает переходы.

> Статус: в разработке, план — в [docs/PLAN.md](docs/PLAN.md).

## Запуск
```bash
docker compose up --build
```
API доступен на `http://localhost:8080`.

## Примеры
```bash
# create a short link
curl -i -X POST http://localhost:8080/links -d '{"url":"https://go.dev"}'
# → 201 {"code":"aB3xK9q","short_url":"http://localhost:8080/aB3xK9q"}

# follow it
curl -i http://localhost:8080/aB3xK9q
# → 302 Location: https://go.dev

# stats
curl http://localhost:8080/links/aB3xK9q
# → {"code":"aB3xK9q","url":"https://go.dev","clicks":1,"created_at":"..."}
```

## Конфигурация
| Переменная | По умолчанию | Описание |
|---|---|---|
| `PORT` | `8080` | Порт HTTP-сервера |
| `DATABASE_URL` | — | Строка подключения к PostgreSQL |
| `BASE_URL` | `http://localhost:8080` | Префикс для `short_url` |

## Архитектура
- `internal/link` — домен: сущность, валидация, генерация кода, сценарии
- `internal/httpapi` — HTTP-слой, без бизнес-логики
- `internal/postgres` — хранилище на pgx
- `cmd/shortener` — сборка и запуск, `cmd/migrate` — миграции

## Тесты
```bash
go test ./...                                   # unit
TEST_DATABASE_URL=postgres://... go test ./...  # + integration
```
