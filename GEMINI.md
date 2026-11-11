# Antigravity Rules for GoAssistant

## Environment & Deployment Context
- **Development vs Production**:
  - Mesin lokal Windows ini **HANYA** digunakan untuk pengeditan kode (development). GoAssistant **TIDAK** berjalan di mesin ini.
  - Lingkungan produksi berjalan di **remote server Linux (VPS Alpine / Debian)** tanpa desktop GUI.
- **Build & Release Automation**:
  - **JANGAN PERNAH** mengompilasi binary Linux secara lokal atau menyuruh pengguna melakukan compile/deploy manual.
  - Kompilasi Linux (`goassistant-linux-amd64`) dan publikasi release ditangani 100% secara otomatis oleh **GitHub Actions** (`.github/workflows/build-linux.yml`) setiap kali commit dan tag baru dibuat melalui `auto_commit.bat`.
  - Server VPS memperbarui dirinya sendiri via perintah `/update` di Telegram yang mengunduh binary rilis dari GitHub.
- **Standar Skrip & Asset Pembantu (Self-Contained Binaries)**:
  - Karena GitHub Releases hanya mendistribusikan **file binary tunggal** `goassistant-linux-amd64` (tanpa folder repository seperti `scripts/`), setiap skrip atau aset pendukung (seperti Python runner, template, atau konfigurasi) **WAJIB di-embed langsung ke dalam binary Go menggunakan `//go:embed`** (contoh: `internal/tools/pypptx` dan `internal/tools/pybrowser`).
  - Sediakan selalu fungsi auto-extract ke folder `data/` atau temporary directory agar binary dapat mengekstrak asetnya sendiri saat pertama kali dijalankan di VPS.
