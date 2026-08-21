# GoAssist Dynamic HTTP (`internal/goassisthttp`)

Modul internal Dynamic HTTP API Server yang terintegrasi langsung dengan binary GoAssistant Core (`goassistant`).

Seluruh rute API, argumen binary CLI, dan mode pagination dapat dikonfigurasi secara dinamis melalui file [configs/endpoints.yaml](file:///d:/project/goassistant/configs/endpoints.yaml) tanpa perlu mengubah kode sumber.

---

## 📁 Struktur File
```
internal/goassisthttp/
├── endpoint_config.go # Parser & validator configs/endpoints.yaml
├── server.go          # Lifecycle manager & dynamic router registration
├── handler.go         # Dynamic HTTP handler (pagination & query flag mapper)
├── runner.go          # Generic CLI command executor via os/exec (timeout 30s)
├── handler_test.go    # Unit tests
└── README.md          # Dokumentasi modul
```

---

## ⚙️ Format Konfigurasi (`configs/endpoints.yaml`)

```yaml
endpoints:
  # --------------------------------------------------------------------------
  # 1. API Pagination
  # Otomatis mengonversi query param:
  # ?page=2&limit=15&select=id,name&filter=status:active&sort=desc&output=json
  # Menjadi:
  # g3a /datafunneling/funneling --filter=status:active --limit=15 --output=json --page=2 --select=id,name --sort=desc
  # --------------------------------------------------------------------------
  - path: "/api/datafunneling/funneling"
    method: "GET"
    binary: "g3a"
    command: "/datafunneling/funneling"
    type: "pagination"                  # Tipe: pagination
    timeout_seconds: 30
    defaults:
      output: "json"
    pagination:
      default_page: 1
      default_limit: 10
      max_limit: 100
      pass_as: "page"                   # "page" (--page=X --limit=Y) atau "offset" (--offset=X --limit=Y)

  # --------------------------------------------------------------------------
  # 2. API Regular (Non-Pagination)
  # --------------------------------------------------------------------------
  - path: "/api/datafunneling/summary"
    method: "GET"
    binary: "g3a"
    command: "/datafunneling/summary"
    type: "regular"                     # Tipe: regular
    timeout_seconds: 30
    defaults:
      output: "json"
```

---

## 💡 Fitur Utama

### 1. Mapping Query Parameters Fleksibel
Semua query parameter yang dikirim oleh client (`select`, `filter`, `sort`, `search`, `order`, custom flag, dll) secara otomatis di-mapping menjadi flag CLI `--<key>=<value>`.

### 2. Mode Pagination (`pass_as: "page"` vs `pass_as: "offset"`)
- `pass_as: "page"`: Menghasilkan flag `--page=X --limit=Y`
- `pass_as: "offset"`: Otomatis menghitung `offset = (page - 1) * limit` dan menghasilkan flag `--offset=X --limit=Y`.

### 3. Route Discovery
Untuk melihat daftar seluruh endpoint yang sedang aktif:
```bash
curl http://localhost:8080/api/routes
```

---

## 📡 Contoh Response API

### Response Endpoint Pagination:
```json
{
  "status": "success",
  "type": "pagination",
  "pagination": {
    "page": 2,
    "limit": 15
  },
  "command": "g3a /datafunneling/funneling --filter=status:active --limit=15 --output=json --page=2 --select=id,name --sort=desc",
  "output": [
    {
      "id": 1,
      "name": "Contoh Data"
    }
  ]
}
```

### Response Endpoint Regular:
```json
{
  "status": "success",
  "type": "regular",
  "command": "g3a /datafunneling/summary --output=json",
  "output": {
    "total_leads": 1250,
    "active_funnels": 8
  }
}
```
