# План MVP

Каждый шаг — отдельная ветка (`task`) и отдельная сессия; после шага всё компилируется и тесты зелёные.

- [x] **1. Скелет.** `cmd/shortener/main.go`: `GET /healthz`, порт из `PORT`, `slog`, graceful shutdown
  (`signal.NotifyContext` + `http.Server.Shutdown`).
  *Тест:* `httptest` — `/healthz` отвечает 200.
- [x] **2. Домен.** `link.New(url)` с валидацией (пусто, `ftp://`, относительный путь, нет хоста, ок);
  генератор кода — 7 символов base62 из `crypto/rand`.
  *Тест:* табличный тест валидации; длина и алфавит кода.
- [x] **3. Сервис.** `Shorten`, `Resolve` (URL + инкремент счётчика), `Stats`; повтор до 3 раз при
  `ErrCodeTaken`; `ErrNotFound`.
  *Тест:* фейковый репозиторий в `_test.go`, фиксированный генератор для проверки повтора.
- [x] **4. HTTP-слой.** `POST /links`, `GET /{code}`, `GET /links/{code}`; JSON; ошибки → 400/404/500;
  `BASE_URL` из env.
  *Тест:* `httptest` на каждый эндпоинт и код ответа.
- [ ] **5. PostgreSQL в Docker.** `docker-compose.yml` с `db`; миграция таблицы `links`
  (`code` UNIQUE, `url`, `clicks`, `created_at`); `cmd/migrate`.
  Решить: goose (библиотека в `cmd/migrate`) или образ `migrate/migrate` в compose.
  *Проверка:* `docker compose up -d db && go run ./cmd/migrate` создаёт таблицу.
- [ ] **6. Репозиторий на pgx.** `Save` (unique_violation 23505 → `ErrCodeTaken`), `FindByCode`,
  атомарный `UPDATE ... SET clicks = clicks + 1 RETURNING url`.
  *Тест:* интеграционные тесты при `TEST_DATABASE_URL`, иначе `t.Skip`.
- [ ] **7. Сборка целиком.** Wiring в `main.go`, многостадийный `Dockerfile`, приложение в compose.
  *Проверка:* `docker compose up` + curl-примеры из README.
- [ ] **8. CI и полировка.** GitHub Actions: vet + test с сервисом postgres; README;
  чек-лист «проект готов» из ROADMAP.

## Ссылки
- Роутинг ServeMux (Go 1.22+): https://go.dev/blog/routing-enhancements
- pgx v5: https://pkg.go.dev/github.com/jackc/pgx/v5
- crypto/rand: https://pkg.go.dev/crypto/rand
- signal.NotifyContext: https://pkg.go.dev/os/signal#NotifyContext
- http.Server.Shutdown: https://pkg.go.dev/net/http#Server.Shutdown
- Коды ошибок PostgreSQL: https://www.postgresql.org/docs/current/errcodes-appendix.html
- goose: https://pkg.go.dev/github.com/pressly/goose/v3
