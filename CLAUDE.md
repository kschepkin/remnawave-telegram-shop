# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Remnawave Telegram Shop is a Go-based Telegram bot for selling VPN subscriptions with Remnawave integration (https://remna.st/). The bot supports multiple payment systems (YooKassa, CryptoPay, Telegram Stars, Tribute) and includes automated subscription management, notifications, trial periods, and referral systems.

**Tech Stack:**
- Language: Go 1.25.3
- Database: PostgreSQL 17 (pgx driver)
- Bot Framework: go-telegram/bot
- Migrations: golang-migrate
- Scheduling: robfig/cron
- Deployment: Docker (multi-platform: linux/amd64, linux/arm64)

## Development Commands

### Running Locally
```bash
# Start services with Docker Compose
docker compose up -d

# Stop services
docker compose down

# View logs
docker logs -f remnawave-telegram-shop-bot
```

### Building

**Development Build:**
```bash
./build-dev.sh
```
Builds and pushes to `ghcr.io/jolymmiels/remnawave-telegram-shop-bot:dev` for both amd64 and arm64.

**Release Build:**
```bash
./build-release.sh
```
Prompts for version (e.g., 3.1.3), then builds and pushes with tags: `<version>`, `<major>`, and `latest`.

**Manual Go Build:**
```bash
go build -ldflags="-w -s -X main.Version=dev -X main.Commit=$(git rev-parse --short HEAD) -X main.BuildDate=$(date -u +'%Y-%m-%dT%H:%M:%SZ')" -o bin/app ./cmd/app
```

### Testing
```bash
# Run tests
go test ./...

# Run specific test
go test ./internal/database -run TestPurchase

# Run with coverage
go test -cover ./...
```

### Database Migrations
Migrations are located in `db/migrations/` and run automatically on startup via `database.RunMigrations()`.

**Manual migration:**
```bash
# Migrations run automatically in main.go
# To create a new migration, add files in db/migrations/ following the naming convention
```

## Architecture

### Entry Point
- `cmd/app/main.go` - Application entry point that initializes all services and starts the bot

### Core Components

**Handler Layer** (`internal/handler/`):
- `handler.go` - Main handler struct with dependencies
- `start.go` - Start command and callback handlers
- `connect.go` - Connection/subscription info handlers
- `payment_handlers.go` - Payment flow handlers (buy, payment selection, pre-checkout)
- `trial.go` - Trial subscription handlers
- `referral.go` - Referral system handlers
- `sync.go` - Admin sync command handler
- `middleware.go` - Auth, suspicious user filtering, customer creation middlewares
- `callback_type.go` - Callback data type constants

**Database Layer** (`internal/database/`):
- Repository pattern with three main repositories:
  - `customer.go` - Customer/user management (Create, Update, FindByTelegramID, etc.)
  - `purchase.go` - Purchase records (Create, FindByID, UpdateStatus, etc.)
  - `referal.go` - Referral tracking
- `persistance.go` - Migration runner and database utilities

**Service Layer:**
- `internal/payment/` - Payment processing service (coordinates payment providers, Remnawave API, purchase lifecycle)
- `internal/notification/` - Subscription expiration notifications (runs daily at 16:00 UTC)
- `internal/sync/` - User synchronization with Remnawave panel
- `internal/translation/` - i18n singleton for Russian/English messages

**Payment Providers:**
- `internal/cryptopay/` - CryptoPay API client
- `internal/yookasa/` - YooKassa API client
- `internal/tribute/` - Tribute webhook handler

**External Integration:**
- `internal/remnawave/` - Remnawave panel API client (user creation, subscription management, squad assignment)

**Configuration:**
- `internal/config/cofig.go` - Environment variable loading and validation (uses godotenv)

**Utilities:**
- `utils/text_sanitizer.go` - HTML sanitization for Telegram messages (prevents injection)
- `utils/utils.go` - General utilities

### Bot Workflow

1. **User Starts Bot** → `StartCommandHandler` processes `/start` command with optional referral code
2. **Middleware Chain**:
   - `SuspiciousUserFilterMiddleware` - Blocks banned users, checks whitelist
   - `CreateCustomerIfNotExistMiddleware` - Creates DB record for new users
3. **Purchase Flow**:
   - User selects subscription duration → `SellCallbackHandler`
   - User selects payment method → `PaymentCallbackHandler`
   - Payment provider creates invoice
   - For Telegram Stars: `PreCheckoutCallbackHandler` → `SuccessPaymentHandler`
   - For CryptoPay/YooKassa: Cron jobs check invoice status every 5 seconds
   - On success: `PaymentService.ProcessPurchaseById()` creates/extends Remnawave user
4. **Trial Flow**:
   - User activates trial → `ActivateTrialCallbackHandler`
   - Creates Remnawave user with trial tag and limited traffic/duration
5. **Notifications**:
   - Daily cron job at 16:00 UTC checks expiring subscriptions
   - Sends reminder 3 days before expiration

### Squad Assignment
- Regular users: assigned to squads from `SQUAD_UUIDS` (internal) + `EXTERNAL_SQUAD_UUID`
- Trial users: assigned to `TRIAL_INTERNAL_SQUADS` + `TRIAL_EXTERNAL_SQUAD_UUID` (fallback to regular if not set)
- Squads are VPN server groups in Remnawave

### Cron Jobs
1. **Invoice Checker** (every 5 seconds if enabled):
   - `checkCryptoPayInvoice()` - Polls CryptoPay for paid invoices
   - `checkYookasaInvoice()` - Polls YooKassa for paid invoices
2. **Subscription Notifications** (daily at 16:00 UTC):
   - `subscriptionChecker()` → `ProcessSubscriptionExpiration()`

### Translation System
- Files: `translations/en.json`, `translations/ru.json`
- Singleton pattern: `translation.GetInstance()`
- All messages support HTML formatting (Telegram HTML tags)
- Default language from `DEFAULT_LANGUAGE` env var

### HTTP Server
Runs on port from `HEALTH_CHECK_PORT` environment variable:
- `GET /healthcheck` - Returns JSON with status of DB, Remnawave panel, version info
- `POST ${TRIBUTE_WEBHOOK_URL}` - Tribute payment webhook (if configured)

### Admin Commands
- `/sync` - Polls Remnawave for users and syncs with database (removes users not in Remnawave)
  - Only accessible to user with `ADMIN_TELEGRAM_ID`

## Environment Configuration

Key variables (full list in README.md):
- **Database**: `DATABASE_URL`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`
- **Telegram**: `TELEGRAM_TOKEN`, `ADMIN_TELEGRAM_ID`
- **Remnawave**: `REMNAWAVE_URL`, `REMNAWAVE_TOKEN`, `REMNAWAVE_MODE`, `REMNAWAVE_TAG`, `X_API_KEY`
- **Pricing**: `PRICE_1`, `PRICE_3`, `PRICE_6`, `PRICE_12`, `STARS_PRICE_*`
- **Payment Providers**: `CRYPTO_PAY_ENABLED`, `YOOKASA_ENABLED`, `TELEGRAM_STARS_ENABLED`
- **Trial**: `TRIAL_DAYS`, `TRIAL_TRAFFIC_LIMIT`, `TRIAL_REMNAWAVE_TAG`, `TRIAL_INTERNAL_SQUADS`
- **Squads**: `SQUAD_UUIDS`, `EXTERNAL_SQUAD_UUID`
- **Referral**: `REFERRAL_DAYS`

## Testing Credentials
- Email: konstantin.schepkin+3@gmail.com
- Password: 4bq6jW9ggoHTHBqGlo#1
- Language: English (application default)

## Important Notes

- **Security**: `utils/text_sanitizer.go` sanitizes all user input before sending to Telegram to prevent XSS/injection
- **Logging**: Use `slog` for structured logging, not `log.Println` (except in legacy code)
- **Error Handling**: Payment errors are logged but don't crash the bot; invoice checkers handle transient failures
- **Idempotency**: Purchase processing checks status before updating to prevent double-processing
- **Deployment**: Uses scratch base image for minimal attack surface (no shell, no package manager)
- **Version Info**: Version, commit, and build date are injected at build time via ldflags and exposed in healthcheck
- **Telegram HTML**: All messages support HTML formatting (https://core.telegram.org/bots/api#html-style)
- **Reverse Proxy**: For Tribute webhooks, requires public domain with SSL (not localhost/IP)

## Code Patterns

**Repository Pattern:**
```go
repo := database.NewCustomerRepository(pool)
customer, err := repo.FindByTelegramID(ctx, telegramID)
```

**Service Layer:**
```go
paymentService.ProcessPurchaseById(ctx, purchaseID)
```

**Translation:**
```go
tm := translation.GetInstance()
message := tm.GetTextByLanguage(language, "key", replacements...)
```

**Middleware:**
```go
b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypePrefix,
    h.StartCommandHandler,
    h.SuspiciousUserFilterMiddleware)
```

## Documentation
- Full documentation: https://remnawave-telegram-shop-bot-doc.vercel.app/
- Remnawave API: https://github.com/Jolymmiles/remnawave-api-go
- Telegram Bot API: https://core.telegram.org/bots/api
