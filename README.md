# Go Wallet Service

Офлайн-сервис HD-кошельков для Ethereum Sepolia на **Fiber v3**
и [Trust Wallet Core](https://github.com/trustwallet/wallet-core).

* генерирует адреса по пути `m/44'/60'/account'/change/address_index`;
* валидирует адреса (wallet-core + проверка EIP-55 checksum);
* подписывает EIP-1559 (type 0x02) транзакции.

Сервис выполняет деривацию и подпись локально, без исходящих соединений и БД. NestJS-сервис управляет адресами,
депозитами и выводами, получает nonce, оценивает газ и комиссии и отправляет подписанные транзакции через
`eth_sendRawTransaction`.

Стек: Go 1.27, `github.com/gofiber/fiber/v3` v3.7, wallet-core (C API через cgo).

---

## 1. Архитектура

### 1.1 Место в системе

```
  Wallets (NestJS) ── HTTP, JSON ──►  Go Wallet Service (127.0.0.1:8000)
   • validateaddress                  • мнемоники из config.json / env — только в памяти
   • createaddress                    • ключи выводятся на время запроса и затираются
   • tx (офлайн-подпись)              • без БД, без исходящих соединений
        │
        └── eth_sendRawTransaction ──► Sepolia RPC   (вызов из NestJS)
```

### 1.2 Слои и поток запроса

```
HTTP ─► Fiber (recover, logger)
          │
          ▼
     routes/api.go ── middleware.ValidateToken (Bearer, если задан auth_token)
          │
          ▼
     middleware.ValidateBody[Request]  ── строгий JSON (неизвестные поля запрещены) + Request.Validate()
          │
          ▼
     handlers/*        ── DTO запроса → вызов сервиса → JSON-ответ
          │
          ▼
     services/*        ── AddressService, TransactionService (логирование операций)
          │
          ▼
     services/chains   ── Registry: gate → Chain;  Ethereum: реализация Chain
          │
          ▼  cgo
     wallet-core C API ── TWHDWallet, TWAnyAddress, TWAnySigner, TWHash
```

Ошибки на любом уровне возвращаются как `error`; единый `routes.ErrorHandler` превращает их в `{"error": "..."}`:
`utils.RequestError` — со своим статусом (400 / 401 / 404 / 415), остальное — 500 с записью в лог.

### 1.3 Ключевые решения

* **Интерфейс `Chain` и реестр.** Хендлеры и сервисы не знают про wallet-core. Новая сеть = новая реализация `Chain` +
  строка в `factories` (`registry.go`); gate выбирает её полем `chain` в config.json (по умолчанию — по имени gate).
* **cgo.** У wallet-core нет Go-библиотеки, только C API к C++/Rust-коду, поэтому вызовы идут через cgo-преамбулу в
  `ethereum.go`.
* **Ручной protobuf.** Подпись в wallet-core принимает сериализованный `TW.Ethereum.Proto.SigningInput`. Сообщение
  кодируется вручную (`utils` + `ethereum.go`), без protoc и сгенерированного кода; кодирование сверено побайтово с
  официальным `Ethereum.proto`.
* **Безопасность ключей.** Ключ выводится на время одного вызова; Go-копия ключа и `SigningInput` затираются, C-буферы
  обнуляются `TWDataReset`. Вызовы wallet-core сериализованы мьютексом.

### 1.4 Структура

```
cmd/
  main.go                       запуск: конфиг, логи, реестр сетей, Fiber, graceful shutdown
internal/
  config/app.go                 .env (пути, логи) + config.json (сети и ключи), секреты из env
  handlers/addreses.go          POST /api/v1/createaddress, /api/v1/validateaddress + DTO
  handlers/transactions.go      POST /api/v1/tx + DTO
  middleware/validation.go      ValidateBody[T], ValidateToken
  routes/api.go                 маршруты и ErrorHandler
  services/chains/chain.go      интерфейс Chain, Path (BIP-44), TxParams и их валидация
  services/chains/ethereum.go   реализация Chain на wallet-core (cgo)
  services/chains/registry.go   gate → Chain
  services/addresses.go         сервис адресов
  services/transactions.go      сервис подписи
  utils/utils.go                ошибки, uint256/hex, строгий JSON, protobuf, .env
logs/                           wallet-service.log, access.log
.env / .env.example             настройки процесса
go.mod
```

---

## 2. Запуск

### 2.1 Требования

* Linux (линковка рассчитана на GNU ld; на macOS собирайте в Docker/Linux-VM);
* Go 1.27 (Fiber v3.7);
* clang (заголовки wallet-core используют `__has_feature`);
* собранный wallet-core.

### 2.2 Сборка wallet-core (один раз, 20–60 минут)

```bash
git clone https://github.com/trustwallet/wallet-core.git ~/wallet-core
cd ~/wallet-core
./bootstrap.sh                                  # зависимости, генерация файлов, Rust-часть
cmake -H. -Bbuild -DCMAKE_BUILD_TYPE=Release
make -Cbuild -j"$(nproc)" TrustWalletCore
```

Альтернатива — собрать официальным Dockerfile репозитория
(`docker build -t wallet-core:local https://github.com/trustwallet/wallet-core.git#master`) и собирать сервис внутри
этого образа; библиотеки будут в `/wallet-core`.

### 2.3 Настройка

`.env` (пути и логи):

```bash
cp .env.example .env
```

```dotenv
APP_CONFIG=config.json   # путь к config.json
LOG_DIR=logs
LOG_LEVEL=info           # debug | info | warn | error
# WALLET_MNEMONIC_ETHEREUM="..."   # перекрывает mnemonic из config.json
# WALLET_AUTH_TOKEN=               # перекрывает auth_token
```

`config.json` (файл в `.gitignore`):

```json
{
  "config": {
    "host": "127.0.0.1",
    "port": 8000
  },
  "gates": [
    {
      "name": "ethereum",
      "mnemonic": "<seed phrase>"
    }
  ]
}
```

* Плейсхолдер `<seed phrase>` сервис отклонит: нужна настоящая мнемоника в файле или в `WALLET_MNEMONIC_ETHEREUM`
  (переменная окружения имеет приоритет).
* Необязательные поля: `config.auth_token` (Bearer-токен; в NestJS — `WALLET_SERVICE_TOKEN`), `gates[].passphrase`
  (BIP-39), `gates[].chain`.
* При запуске на одном хосте с NestJS используйте `127.0.0.1:8000`. При работе в контейнерах настройте внутреннюю сеть и
  адрес прослушивания так, чтобы сервис был доступен только доверенному приложению.
* В NestJS задайте `WALLET_SERVICE_URL` — адрес Go-сервиса — и `WALLET_SERVICE_TOKEN`, совпадающий с его Bearer-токеном.
  Имя gate `ethereum` используется для деривации и подписи; сетевые настройки и RPC хранятся в NestJS-сервисе.

### 2.4 Сборка и запуск

```bash
export WALLET_CORE=~/wallet-core
export CC=clang CGO_ENABLED=1
export CGO_CFLAGS="-I$WALLET_CORE/include"
export CGO_LDFLAGS="-L$WALLET_CORE/build -L$WALLET_CORE/build/local/lib -L$WALLET_CORE/build/trezor-crypto -L$WALLET_CORE/rust/target/release"

go mod tidy                                     # создаст go.sum
go build -trimpath -o bin/wallet-service ./cmd
./bin/wallet-service
```

Остановка — `Ctrl+C` / `SIGTERM`: сервис дожидается текущих запросов (до 10 с) и освобождает кошельки.

### 2.5 Проверка

```bash
curl -s localhost:8000/health
# {"status":"ok"}

curl -s -H 'Content-Type: application/json' \
  -d '{"gate":"ethereum","account":0,"change":0,"address_index":0}' \
  localhost:8000/api/v1/createaddress
```

С тестовой мнемоникой `test test test test test test test test test test test junk` адрес по пути `m/44'/60'/0'/0/0`
должен быть `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` — так можно убедиться, что wallet-core собран и подключён
правильно. Эту мнемонику нельзя использовать для реальных средств.

---

## 3. API

Все запросы — `POST` с `Content-Type: application/json`; неизвестные поля в теле запрещены. Ошибки — `{"error": "..."}`:

| Код | Когда                                                   |
|-----|---------------------------------------------------------|
| 400 | неверный JSON или параметры                             |
| 401 | задан `auth_token`, а `Authorization: Bearer …` неверен |
| 404 | неизвестный gate                                        |
| 415 | не `application/json`                                   |
| 500 | внутренняя ошибка (детали только в логе)                |

### POST /api/v1/createaddress

```json
{
  "gate": "ethereum",
  "account": 0,
  "change": 0,
  "address_index": 15
}
```

```json
{
  "gate": "ethereum",
  "account": 0,
  "change": 0,
  "address_index": 15,
  "path": "m/44'/60'/0'/0/15",
  "address": "0x…(EIP-55)"
}
```

### POST /api/v1/validateaddress

```json
{
  "gate": "ethereum",
  "address": "0x…"
}
```

```json
{
  "valid": true,
  "address": "0x…(EIP-55)"
}
```

Невалидный адрес — `200 {"valid": false}`, а не ошибка.

### POST /api/v1/tx (offline only)

```json
{
  "gate": "ethereum",
  "account": 0,
  "change": 0,
  "address_index": 15,
  "tx_params": {
    "to": "0x…",
    "value_wei": "0",
    "data": "0x…",
    "nonce": 13,
    "chain_id": 11155111,
    "gas_limit": 90000,
    "max_fee_per_gas_wei": "30000000000",
    "max_priority_fee_per_gas_wei": "1500000000"
  }
}
```

```json
{
  "tx_hash": "0x…",
  "signed_tx": "0x02f8…",
  "from": "0x…"
}
```

* суммы и комиссии — десятичные строки uint256; `nonce`, `chain_id`, `gas_limit` — целые;
* одна схема для ETH (`data = "0x"`) и ERC-20 (`to` = контракт, `value_wei = "0"`, `data` = calldata `transfer`);
* `tx_hash = keccak256(signed_tx)`; `from` — адрес ключа (NestJS сверяет его с горячим кошельком).

### GET /health

`{"status":"ok"}`

---
