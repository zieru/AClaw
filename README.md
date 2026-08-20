# GoAssistant 🤖🚀

Alternatif **OpenClaw** modern berbasis **Golang murni (Pure Go / Zero-CGO)**. Dirancang ultra-ringan (RAM ~15-35MB), handal, dan **dikontrol 100% melalui Telegram** tanpa memerlukan Web UI.

Sistem ini menjamin **100% kompatibilitas dengan GLIBC versi lama** (`pypa/manylinux_2_28_x86_64`, CentOS 8, RHEL 8, Debian 10/11, Alpine Linux, dsb.) karena dikompilasi secara statis tanpa ketergantungan GCC/CGO host.

---

## 🌟 Fitur Utama

1. **🤖 Multi-Provider AI Hub (Termasuk 9Router)**
   - Integrasi langsung dengan **9Router**, OpenAI, Anthropic Claude, Google Gemini, Groq, DeepSeek, Ollama, dan Custom OpenAI-compatible endpoints.
   - Manajemen API Key, default model, parameter, dan sistem *auto-fallback* jika salah satu provider error/rate-limited.
   - **Efisiensi WebSearch Upstream**: Websearch dapat dibebankan langsung ke 9Router tanpa beban scraping internal.

2. **🛡️ Hierarchical Governance & Limits (Global, Channel, Group Chat)**
   - Atur batas ukuran upload file (`max_upload_file_mb`), batas token (`max_tokens`), dan batas riwayat chat (`max_history_turns`).
   - **Auto-Compaction**: Percakapan yang melebihi batas pesan secara otomatis diringkas (*compacted*) menggunakan AI, menghemat context window dan biaya token.
   - Pewarisan aturan: `Global Setting` ➔ `Channel Setting` ➔ `Specific Group/Chat Override`.

3. **📱 Multi-Channel Engine (Telegram & WhatsApp)**
   - Dukungan multiple instance bot Telegram (Admin Bot & Public/Group Bots).
   - Dukungan channel WhatsApp (Webhook Bridge / Direct API).
   - Mode interaksi grup (Mention-only vs All-messages).

4. **📝 Markdown Persona Engine (.MD Hot-Reloading)**
   - Kelola persona dan SOP bot melalui file `.md`:
     - `IDENTITY.md`: Persona, gaya bicara, dan bahasa.
     - `AGENTS.md`: Definisi sub-agent spesifik (`@coder`, `@researcher`, `@secretary`).
     - `TOOLS.md`: Aturan dan panduan penggunaan alat.
     - `SOUL.md`: Basis etika dan prinsip respon.
   - Kirim file `.md` langsung ke chat Telegram Admin untuk update instan tanpa restart.

5. **🧰 Dynamic Tool Security Matrix**
   - Batasi akses tools per-channel (misal: channel publik dilarang menjalankan perintah terminal `bash_exec`, channel admin diizinkan penuh).

6. **⏰ Cron Task Scheduler**
   - Jalankan tugas AI otomatis dan kirimkan pesan proaktif ke grup atau user tertentu berdasarkan jadwal cron (`robfig/cron/v3`).

7. **🧠 Memory Management & Session Tracking**
   - Penyimpanan riwayat sesi percakapan, sliding window buffer, auto-summarizer, dan memori profil jangka panjang di SQLite.

8. **📊 Audit Log & Token Tracker**
   - Pencatatan seluruh request/response, token in/out, latency, status, tools yang dipanggil, dan estimasi biaya.
   - Laporan ringkas `/stats`, `/logs`, dan fitur ekspor CSV langsung ke chat Telegram.

9. **💾 One-Click Backup & Restore**
   - Perintah `/backup` langsung mengirimkan file archive `.zip` berisi database SQLite dan seluruh file `.md` ke Telegram Admin.

---

## 📂 Struktur Direktori

```
goassistant/
├── cmd/
│   └── goassistant/
│       └── main.go                 # Entrypoint daemon
├── configs/
│   └── default_config.yaml         # Konfigurasi server & token
├── data/
│   ├── md/                         # File Markdown persona & knowledge
│   │   ├── IDENTITY.md
│   │   ├── AGENTS.md
│   │   ├── TOOLS.md
│   │   └── SOUL.md
│   └── goassistant.db             # Database SQLite Pure Go
├── internal/
│   ├── admin/                      # Control Plane Telegram Bot (/menu, commands, wizards)
│   ├── agent/                      # Orchestrator, Prompt Builder, Policy Engine
│   ├── audit/                      # Token Tracker & Audit Logger
│   ├── channel/                    # Adapter Telegram & WhatsApp
│   ├── config/                     # Configuration Manager
│   ├── cron/                       # Cron Task Scheduler
│   ├── memory/                     # Memory & Session Manager
│   ├── provider/                   # 9Router, OpenAI, Gemini, Claude Providers
│   ├── storage/                    # Pure Go SQLite Storage (modernc.org/sqlite)
│   └── tools/                      # Tool Definitions & Registry
├── Makefile                        # Multi-platform static build scripts
├── go.mod
└── go.sum
```

---

## 🚀 Panduan Memulai

### 1. Konfigurasi
Buka file `configs/default_config.yaml` dan masukkan Token Bot Telegram Admin yang didapat dari `@BotFather`:
```yaml
admin_telegram:
  bot_token: "123456789:ABCDefGhIjKlMnOpQrStUvWxYz"
  allowed_user_ids: [] # Masukkan Telegram ID Anda, atau kosongkan untuk user pertama
```

### 2. Kompilasi & Menjalankan

**Untuk Development Lokal:**
```bash
go run ./cmd/goassistant -config configs/default_config.yaml
```

**Untuk Deployment Linux (manylinux_2_28 / Statically Linked Zero-CGO):**
```bash
make build-linux-static
```
Binary statis akan dibuat di `dist/goassistant-linux-amd64` dan dapat langsung dijalankan di semua distribusi Linux (RHEL 8, CentOS 8, Ubuntu, Debian, Alpine) tanpa error glibc!

---

## 🎛️ Panduan Perintah Telegram Admin

Ketik `/menu` di chat bot Telegram Admin untuk membuka dashboard tombol interaktif:

| Kategori | Command | Deskripsi |
|---|---|---|
| **Dashboard** | `/menu` | Membuka dashboard kontrol utama |
| **Provider** | `/providers` | Menampilkan daftar provider AI aktif |
| | `/addprovider` | Menambah provider baru (9Router, OpenAI, Gemini, dll) |
| | `/setkey` | Mengatur API Key provider |
| | `/setmodel` | Mengubah default model provider |
| **Pembatasan** | `/limits` | Melihat ringkasan batas upload & token |
| | `/setlimit` | Mengatur batas upload, token, history, & auto-compaction |
| **Channels** | `/channels` | Melihat dan menambah channel bot |
| | `/tools` | Melihat seluruh tool sistem yang tersedia |
| | `/toolperms` | Mengatur izin tool per channel |
| **Markdown** | `/md` | Melihat daftar file `.md` bot |
| | `/viewmd` | Membaca isi file `.md` |
| | `/editmd` | Mengedit isi file `.md` langsung via chat |
| **Cron** | `/cron` | Menampilkan daftar tugas terjadwal |
| | `/addcron` | Mendaftarkan cron job baru |
| | `/runcron` | Menjalankan cron job detik ini juga |
| **Memori** | `/memory` | Menampilkan memori profil & SOP |
| | `/savefact` | Menyimpan fakta permanen ke sistem |
| | `/resetsession` | Membersihkan riwayat percakapan sesi |
| **Audit** | `/stats` | Laporan token, request, biaya hari ini |
| | `/logs` | Menampilkan 10 request terakhir |
| | `/exportlogs` | Mengunduh riwayat audit dalam format `.csv` |
| | `/backup` | Mengunduh backup `.zip` database & file `.md` |
