# GoAssistant 🤖🚀

Alternatif **OpenClaw** modern berbasis **Golang murni (Pure Go / Zero-CGO)**. Dirancang ultra-ringan (RAM ~15-35MB), handal, dan **dikontrol 100% melalui Telegram Control Plane** tanpa memerlukan Web UI.

Sistem ini menjamin **100% kompatibilitas dengan GLIBC versi lama** (`pypa/manylinux_2_28_x86_64`, CentOS 8, RHEL 8, Debian 10/11, Alpine Linux, dsb.) karena dikompilasi secara statis tanpa ketergantungan GCC/CGO host.

---

## 🌟 Fitur Utama

1. **🤖 Multi-Provider AI Hub & Dynamic Switcher**
   - Integrasi langsung dengan **9Router**, OpenAI, Anthropic Claude, Google Gemini (Official API), **Gemini Web (Scraper dengan Auto-Refresh Token)**, Groq, DeepSeek, Ollama, Free Routers, dan Custom OpenAI-compatible endpoints.
   - Manajemen API Key (Single & Multi-key load balancing / failover), default model, parameter, dan sistem *auto-fallback* jika salah satu provider error/rate-limited.
   - Dukungan **Model Combos**: Alur pipeline fallback multi-model (misal: Coba Claude 3.5 Sonnet -> Fallback ke Gemini 2.0 Flash -> Fallback ke DeepSeek V3).

2. **🌐 Tavily AI Real-Time Web Search**
   - Pencarian internet terstruktur berbasis AI khusus untuk LLM Agent.
   - Konfigurasi parameter `search_depth` (*basic* / *advanced*) dan `max_results` langsung dari bot Telegram (`/tavily`).

3. **🎁 Auto Check-In HCNSEC (New API Quota Claim)**
   - Fitur absensi otomatis harian ke `https://api.hcnsec.cn/api/user/checkin` untuk klaim bonus kuota saldo gratis.
   - Mendukung multi-akun dengan auto-login (`username:password` atau `email:password`) serta manual session cookie.
   - Pengecekan saldo sebelum dan sesudah checkin, konversi kuota ke nilai Dollar ($), dan pengiriman notifikasi rekap harian ke Telegram Admin pukul 00:05 WIB.

4. **🌿 Token Saver & Prompt Compression (RTK Engine)**
   - Algoritma kompresi prompt dinamis untuk menghemat konsumsi token input hingga 30-50%.
   - Mode fleksibel: `off`, `auto`, `aggressive`, dan `caveman`.

5. **🛡️ Hierarchical Governance & Limits (Global, Channel, Group Chat)**
   - Atur batas ukuran upload file (`max_upload_file_mb`), batas token (`max_tokens`), dan batas riwayat chat (`max_history_turns`).
   - **Auto-Compaction**: Percakapan yang melebihi batas pesan secara otomatis diringkas (*compacted*) menggunakan AI, menghemat context window dan biaya token.
   - Mode footer respon: `off`, `tokens`, atau `full`.

6. **📱 Multi-Channel Engine (Telegram & WhatsApp Native)**
   - Dukungan multiple instance bot Telegram (Admin Control Plane & Public/Group Bots).
   - Dukungan channel WhatsApp Native (Multi-Device connection).
   - Mode interaksi grup (Mention-only vs All-messages).

7. **📝 Markdown Persona Engine (.MD Hot-Reloading)**
   - Kelola persona dan SOP bot melalui file `.md`:
     - `IDENTITY.md`: Persona, gaya bicara, dan bahasa.
     - `AGENTS.md`: Definisi sub-agent spesifik (`@coder`, `@researcher`, `@secretary`).
     - `TOOLS.md`: Aturan dan panduan penggunaan alat.
     - `SOUL.md`: Basis etika dan prinsip respon.
   - Kirim file `.md` langsung ke chat Telegram Admin untuk update instan tanpa restart.

8. **🌐 Upstream Proxy Pool Manager**
   - Pengelolaan upstream HTTP/SOCKS5 proxy pool untuk routing provider dan bypass geo-blocking.

9. **⏰ Cron Task Scheduler**
   - Jalankan tugas AI otomatis dan kirimkan pesan proaktif ke grup atau user tertentu berdasarkan jadwal cron (`robfig/cron/v3`).

10. **🧠 Memory Management & Session Tracking**
    - Penyimpanan riwayat sesi percakapan, sliding window buffer, auto-summarizer, dan memori profil jangka panjang di SQLite.

11. **📊 Audit Log & Token Tracker**
    - Pencatatan seluruh request/response, token in/out, latency, status, tools yang dipanggil, dan estimasi biaya.
    - Laporan ringkas `/stats`, `/logs`, dan fitur ekspor CSV langsung ke chat Telegram.

12. **🚀 System Auto-Updater & One-Click Backup**
    - Perintah `/update` untuk cek dan update binary langsung dari GitHub Releases.
    - Perintah `/backup` langsung mengirimkan file archive `.zip` berisi database SQLite dan seluruh file `.md` ke Telegram Admin.

13. **🔌 GoAssist HTTP API Server (Dynamic CLI Gateway)**
    - Server HTTP REST API internal (`configs/endpoints.yaml`) untuk meneruskan request ke binary CLI lokal dengan pagination otomatis.

---

## 📂 Struktur Direktori

```
goassistant/
├── cmd/
│   └── goassistant/
│       └── main.go                 # Entrypoint daemon
├── configs/
│   ├── default_config.yaml         # Konfigurasi server, bot & token
│   └── endpoints.yaml              # Definisi REST API dinamis
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
│   ├── channel/                    # Adapter Telegram & WhatsApp Native
│   ├── checkin/                    # HCNSEC Auto Check-in & Quota Tracker
│   ├── config/                     # Configuration Manager
│   ├── cron/                       # Cron Task Scheduler
│   ├── goassisthttp/               # HTTP API Server Gateway
│   ├── memory/                     # Memory & Session Manager
│   ├── provider/                   # 9Router, OpenAI, Gemini, Gemini Web, Claude Providers
│   ├── proxy/                      # Proxy Pool & Upstream Manager
│   ├── storage/                    # Pure Go SQLite Storage (modernc.org/sqlite)
│   ├── tokensaver/                 # Prompt Compression & Token Saver Engine
│   ├── tools/                      # Tool Definitions & Registry
│   └── updater/                    # GitHub Releases Auto-Updater
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
  allowed_user_ids: [123456789] # Masukkan Telegram ID Anda
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
| **Dashboard & Konteks** | `/menu` | Membuka dashboard kontrol interaktif |
| | `/status` | Ringkasan operasional runtime, RAM, DB & AI engine |
| | `/new` (atau `/reset`) | Memulai sesi baru & mereset riwayat konteks percakapan |
| | `/stop` (atau `/cancel`) | Menghentikan respon AI atau membatalkan proses wizard |
| | `/help` | Menampilkan panduan lengkap seluruh perintah |
| **Model & Provider** | `/model` | Memilih model AI aktif atau beralih ke Fallback Combo |
| | `/providers` | Menampilkan daftar provider AI aktif |
| | `/wizard` (atau `/setup`)| Menambah provider AI baru via interaktif wizard |
| | `/editprovider` | Mengubah setting / API key / model provider |
| | `/gemini_login` | Login sesi Google Web Scrape via cookie |
| | `/combos` | Menampilkan & mengelola Model Fallback Combos |
| **Web Search & Check-in** | `/tavily` | Konfigurasi Tavily AI Real-Time Web Search |
| | `/checkin` | Dashboard Auto Check-in HCNSEC (New API) |
| | `/checkin_run` | Menjalankan eksekusi check-in & cek saldo sekarang |
| | `/checkin_add` | Mendaftarkan akun check-in (`/checkin_add user:pass`) |
| | `/checkin_del` | Menghapus akun check-in (`/checkin_del username`) |
| **Token & Limits** | `/tokensaver` | Konfigurasi Token Saver & Kompresi Prompt |
| | `/limits` | Melihat ringkasan batas upload & token |
| | `/setlimit` | Mengatur batas upload, token, history, & auto-compaction |
| **Channels & Tools** | `/channels` | Melihat dan menambah channel bot Telegram/WhatsApp |
| | `/tools` | Melihat seluruh tool sistem yang tersedia |
| | `/toolperms` | Mengatur izin tool per channel |
| **Markdown** | `/md` | Melihat daftar file `.md` persona bot |
| | `/viewmd` | Membaca isi file `.md` |
| | `/editmd` | Mengedit isi file `.md` langsung via chat |
| | `/reloadmd` | Memuat ulang seluruh cache file `.md` |
| **Cron Scheduler** | `/cron` | Menampilkan daftar tugas terjadwal |
| | `/addcron` | Mendaftarkan cron job baru |
| | `/runcron` | Menjalankan cron job detik ini juga |
| | `/delcron` | Menghapus jadwal cron job |
| **Memori & Profil** | `/memory` | Menampilkan memori profil & SOP bot |
| | `/savefact` | Menyimpan fakta permanen ke memori sistem |
| | `/resetsession` | Membersihkan riwayat percakapan sesi |
| **Audit & Sistem** | `/stats` | Laporan token, request, biaya hari ini |
| | `/logs` | Menampilkan riwayat request terakhir |
| | `/exportlogs` | Mengunduh riwayat audit dalam format `.csv` |
| | `/backup` | Mengunduh backup `.zip` database & file `.md` |
| | `/update` | Cek & pasang update binary dari GitHub Releases |
| | `/proxies` | Mengelola upstream proxy pool |
