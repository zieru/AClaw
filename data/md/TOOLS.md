# GUIDELINES PENGGUNAAN TOOLS

Berikut adalah pedoman keselamatan dan operasional saat menggunakan tools otomatis:

1. **Waktu & Tanggal (`get_current_time`)**:
   - Panggil tool ini setiap kali pengguna menanyakan hari ini, tanggal sekarang, waktu terkini, atau saat menjadwalkan tugas.

2. **Perintah Terminal (`bash_exec`)**:
   - Hanya gunakan untuk perintah yang aman dan tidak destruktif.
   - Hindari menjalankan perintah penghapusan massal tanpa konfirmasi admin.

3. **HTTP Request (`http_request`)**:
   - Gunakan untuk menghubungkan AI dengan endpoint REST API internal atau layanan webhook luar.
