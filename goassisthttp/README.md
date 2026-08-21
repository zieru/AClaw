# GoAssist HTTP (`goassist http`)

HTTP API Server wrapper berbasis standard library Go (`net/http`) untuk mengeksekusi perintah CLI `g3a /datafunneling/funneling`.

---

## 📁 Struktur Folder
```
goassisthttp/
├── main.go        # Entry point, konfigurasi port, routing & setup server
├── handler.go     # HTTP handler GET /api/datafunneling/funneling
├── runner.go      # Fungsi eksekutor binary os/exec dengan timeout
├── handler_test.go# Unit testing handler
├── go.mod         # Module definition
└── README.md      # Dokumentasi & panduan penggunaan
```

---

## 🚀 Cara Menjalankan Server

### 1. Menjalankan Langsung dengan `go run`
Masuk ke folder `goassisthttp` dan jalankan:
```bash
cd goassisthttp
go run .
```
Atau tentukan custom port:
```bash
go run . -port 8080
```

### 2. Build Binary
```bash
cd goassisthttp
go build -o goassisthttp.exe .
./goassisthttp.exe -port 8080
```

---

## 📡 Dokumentasi Endpoint

### **GET `/api/datafunneling/funneling`**

#### **Query Parameters (Opsional)**:
| Parameter | Tipe | Deskripsi | Flag yang Dihasilkan |
|---|---|---|---|
| `select` | string | Memilih kolom/field tertentu | `--select=<value>` |
| `limit` | string/integer | Batas jumlah data | `--limit=<value>` |
| `output` | string | Format output (`json`, `ton`, `table`) | `--output=<value>` |

---

## 💡 Contoh Request & Response

### Contoh 1: Request dengan semua parameter (Output JSON)
```bash
curl -X GET "http://localhost:8080/api/datafunneling/funneling?select=id,name,status&limit=10&output=json"
```

**Perintah binary yang dieksekusi:**
```bash
g3a /datafunneling/funneling --select=id,name,status --limit=10 --output=json
```

**Contoh Response Sukses (200 OK):**
```json
{
  "status": "success",
  "command": "g3a /datafunneling/funneling --select=id,name,status --limit=10 --output=json",
  "output": [
    {
      "id": 1,
      "name": "Data A",
      "status": "active"
    }
  ]
}
```

---

### Contoh 2: Request hanya dengan parameter `limit`
```bash
curl -X GET "http://localhost:8080/api/datafunneling/funneling?limit=5"
```

**Perintah binary yang dieksekusi:**
```bash
g3a /datafunneling/funneling --limit=5
```

---

### Contoh 3: Output Format `table` / `ton` (Plain Text)
```bash
curl -X GET "http://localhost:8080/api/datafunneling/funneling?limit=5&output=table"
```

**Contoh Response Sukses (200 OK):**
```json
{
  "status": "success",
  "command": "g3a /datafunneling/funneling --limit=5 --output=table",
  "output": "+----+--------+--------+\n| ID | NAME   | STATUS |\n+----+--------+--------+\n| 1  | Data A | active |\n+----+--------+--------+"
}
```

---

### Contoh 4: Respons Error (Jika Binary Gagal / Timeout)
```json
{
  "status": "error",
  "command": "g3a /datafunneling/funneling --limit=10",
  "message": "eksekusi binary gagal: exit status 1 (output: error: connection refused)"
}
```
