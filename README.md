# Backend PRIMS

API REST untuk menerbitkan, membagikan, dan mengaudit akses dokumen internal anak perusahaan PUSRI. Metadata dan log akses disimpan di SQL Server; berkas PDF disimpan di MinIO.

[Setup lokal](#setup-lokal) · [Coba API](#coba-api) · [Referensi API](#referensi-api) · [Pengembangan](#pengembangan) · [Pemecahan masalah](#pemecahan-masalah)

## Fitur

- **Administrasi perusahaan:** kelola akun, kategori, tag, dan dokumen. Admin anak perusahaan mengelola data perusahaan tempat akun terdaftar.
- **Portal dokumen:** telusuri dokumen perusahaan dan lihat dokumen privat yang dibagikan ke akun Anda.
- **Akses PDF:** baca melalui sesi viewer atau unduh salinan dengan watermark identitas pengunduh. Aktivitas baca dan unduh dicatat dalam log akses.

Backend memakai Go 1.27.0+, Gin, GORM, `golang-jwt/v5` untuk autentikasi, dan pdfcpu untuk watermark PDF.

## Setup lokal

Siapkan [Go 1.27.0+](https://go.dev/dl/), [Git](https://git-scm.com/downloads), SQL Server, dan MinIO. [Docker](https://docs.docker.com/get-docker/) opsional untuk menjalankan SQL Server. Contoh perintah berikut memakai Bash.

### 1. Clone repositori

```bash
git clone https://gitlab.pusri.co.id/prims/be-prims.git
cd be-prims
```

### 2. Buat database SQL Server

Jika sudah punya instance SQL Server, buat database `prims` dengan `CREATE DATABASE prims;`, lalu sesuaikan koneksi di `.env`. Jika belum, jalankan SQL Server 2022 di Docker:

```bash
docker run --name prims-sqlserver \
  -e ACCEPT_EULA=Y \
  -e 'MSSQL_SA_PASSWORD=PrimsLocal_ChangeMe123' \
  -p 127.0.0.1:1433:1433 \
  -v prims-sql-data:/var/opt/mssql \
  -d mcr.microsoft.com/mssql/server:2022-latest
```

Setelah SQL Server siap menerima koneksi (`docker logs prims-sqlserver`), buat database satu kali:

```bash
docker exec -it prims-sqlserver /opt/mssql-tools18/bin/sqlcmd \
  -S localhost -U sa -P 'PrimsLocal_ChangeMe123' -C \
  -Q 'CREATE DATABASE prims;'
```

### 3. Jalankan MinIO

Lewati langkah ini jika sudah punya instance MinIO. Jika belum, jalankan dari root repositori di terminal terpisah:

```bash
go install github.com/minio/minio@latest
MINIO_ROOT_USER=minioadmin MINIO_ROOT_PASSWORD=minioadmin \
  "$(go env GOPATH)/bin/minio" server ./minio-data \
  --address '127.0.0.1:9000' --console-address '127.0.0.1:9001'
```

Konsol MinIO tersedia di <http://localhost:9001>. Backend memeriksa dan membuat bucket `prims` saat startup jika bucket belum ada.

### 4. Buat `.env`

Buat `.env` di root repositori. Contoh ini cocok dengan layanan lokal di atas; sesuaikan kredensial dan endpoint jika memakai instance lain.

```dotenv
SQL_SERVER_URL=sqlserver://sa:PrimsLocal_ChangeMe123@localhost:1433?database=prims
JWT_SECRET=replace-with-a-long-random-secret
MINIO_ENDPOINT=localhost:9000
MINIO_ACCESS_KEY=minioadmin
MINIO_SECRET_KEY=minioadmin
MINIO_BUCKET=prims
MINIO_USE_SSL=false
```

Ganti `JWT_SECRET` dengan nilai acak. Isi `MINIO_ENDPOINT` dalam format `host:port` tanpa skema, dan set `MINIO_USE_SSL=true` jika koneksi ke MinIO memakai TLS.

| Variabel opsional | Kegunaan |
| --- | --- |
| `VIEW_TOKEN_SECRET` | Secret untuk token sesi viewer. Jika kosong, memakai `JWT_SECRET`; pisahkan keduanya di lingkungan non-lokal. |
| `TRUSTED_PROXIES` | CIDR reverse proxy tepercaya, dipisahkan koma. Jika kosong, log akses memakai IP koneksi langsung. |

> [!IMPORTANT]
> Jangan commit `.env`. Kredensial contoh dan akun seed hanya untuk pengembangan lokal; jangan mengekspos server demo ke jaringan publik.

### 5. Jalankan server dan isi data demo

```bash
go run .
```

Server menjalankan migrasi tabel, menginisialisasi MinIO, lalu melayani permintaan di <http://localhost:8080>. Setelah server siap, jalankan perintah berikut dari terminal lain di root repositori:

```bash
go run . seed
```

Seed mengisi data referensi, akun demo, dan metadata dokumen. Tidak perlu me-restart server.

## Coba API

### Login sebagai admin demo

```bash
API=http://localhost:8080/api/v1
curl -fsS "$API/company"
```

Cari perusahaan berkode `SMR`. Ganti `1` pada contoh berikut dengan nilai `id` perusahaan tersebut jika berbeda:

```bash
curl -fsS "$API/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"badge_number":"1001","password":"password123","company_id":1}'
```

Salin `data.token` dari respons, lalu gunakan untuk memeriksa profil:

```bash
TOKEN='paste-data.token-here'
curl -fsS "$API/auth/me" -H "Authorization: Bearer $TOKEN"
```

### Unggah dan unduh PDF

Seed **tidak** mengunggah PDF ke MinIO. Untuk mencoba alur berkas, unggah PDF sendiri dengan token admin. Ambil ID dari `GET /category`, `GET /departments`, dan `GET /tags`, lalu ganti nilai contoh `1` di bawah. Ganti `contoh.pdf` dengan path berkas Anda.

```bash
curl -fsS "$API/document" -H "Authorization: Bearer $TOKEN" \
  -F 'title=Panduan Arsip' -F 'description=Dokumen uji lokal' \
  -F 'document_number=PRIMS-001' -F 'publication_date=2026-01-15' \
  -F 'category_id=1' -F 'department_id=1' -F 'tag_id=1' \
  -F 'allow_download=true' -F 'attachment=@contoh.pdf'
```

Gunakan `data.uuid` yang dikembalikan untuk mengunduh salinan ber-watermark:

```bash
DOCUMENT_UUID='paste-data.uuid-here'
curl -fsS "$API/portal/documents/$DOCUMENT_UUID/download" \
  -H "Authorization: Bearer $TOKEN" -o unduhan-prims.pdf
```

### Akun demo

Semua akun seed memakai password `password123`.

| Nomor badge | Role | Kode perusahaan |
| --- | --- | --- |
| `1000` | `SUPERADMIN` | `SMR` |
| `1001` | `ADMIN_ANPER` | `SMR` |
| `1002`, `1003` | `KARYAWAN` | `SMR` |
| `2001` | `KARYAWAN` | `PAL` |

## Referensi API

Semua rute berikut memakai prefix `/api/v1`. Rute terproteksi menerima header `Authorization: Bearer <token>`.

| Rute | Kegunaan |
| --- | --- |
| `GET /company`, `POST /auth/login` | Daftar perusahaan dan login; tanpa token |
| `GET /auth/me` | Profil akun yang sedang login |
| `GET /superadmin/companies` | `SUPERADMIN`: daftar semua perusahaan dan statistik; filter `search`, `status`, `page`, `limit` |
| `POST /superadmin/companies` | `SUPERADMIN`: daftarkan perusahaan dan siapkan folder MinIO |
| `PUT /superadmin/companies/:id` | `SUPERADMIN`: ubah nama, kode, alamat, dan kuota |
| `PATCH /superadmin/companies/:id/status` | `SUPERADMIN`: ubah status dengan `{"status":"active"}` atau `{"status":"inactive"}` |
| `PATCH /superadmin/anper-admins/:id/reset-password` | `SUPERADMIN`: reset password akun `ADMIN_ANPER`. Kirim `{"new_password":"<password baru>"}` (maks. 72 byte). Reset dicatat di `audit_logs`. |
| `PATCH /superadmin/anper-admins/:id/status` | `SUPERADMIN`: ubah status akun `ADMIN_ANPER` dengan `{"status":"active"}` atau `{"status":"inactive"}`. Akun nonaktif tidak bisa login; token yang sudah terbit tidak dicabut oleh endpoint ini. |
| `GET /portal/categories`, `GET /portal/documents` | Kategori dan daftar dokumen yang dapat diakses |
| `GET /portal/documents/shared-with-me` | Dokumen yang dibagikan ke akun |
| `GET /portal/documents/:uuid` | Detail dokumen |
| `POST /portal/documents/:uuid/view-session` | Buat token sementara untuk membaca PDF |
| `GET /portal/documents/:uuid/stream` | Stream PDF dengan Bearer JWT atau `?view_token=…` |
| `GET /portal/documents/:uuid/download` | Unduh PDF berstempel identitas dengan nama file dari judul dokumen. `allow_download` harus aktif; untuk dokumen rahasia, termasuk bagi `ADMIN_ANPER`, pengguna harus terdaftar sebagai penerima share. |
| `POST /document` | `ADMIN_ANPER`: unggah satu PDF lewat `multipart/form-data`, field `attachment`, maksimum 25 MiB |
| `GET /documents`, `GET /admin/audit-logs` | `ADMIN_ANPER`: daftar dokumen dan log akses perusahaan |

POST dan PUT perusahaan menerima `name`, `code`, `storage_quota_gb`, dan `address`. Kode unik tanpa membedakan huruf besar-kecil. Kuota default perusahaan baru adalah 50; satu unit GB di API = 1.073.741.824 byte. Pemakaian dihitung dari ukuran lampiran dokumen yang tercatat, bukan seluruh isi bucket. Upload yang melampaui kuota ditolak dengan `409` (`storage_quota_exceeded`); menurunkan kuota tidak menghapus dokumen.

Perusahaan nonaktif tetap terlihat di dropdown login supaya pengguna dapat menerima pesan yang sesuai. Token API dan viewer lama milik karyawan atau Admin Anper ditolak, termasuk setelah perusahaan diaktifkan kembali. Superadmin tetap dapat mengelola perusahaan. Perubahan kode atau status tidak memindahkan atau menghapus dokumen lama.

Lihat [`routes/route.go`](routes/route.go) untuk semua rute dan aturan aksesnya. PDF disajikan lewat endpoint portal, bukan URL statis `/uploads`.

## Pengembangan

```bash
go test ./...
go build -o be-prims .
```

Untuk tes integrasi perusahaan, isi `PRIMS_TEST_SQLSERVER_URL` dengan koneksi ke **database uji kosong**, atur `MINIO_*` ke bucket uji, lalu jalankan `go test ./routes -run TestCompanyLifecycleIntegration -count=1`. Tes membuat tabel dan menyimpan PDF uji.

| Perintah | Fungsi |
| --- | --- |
| `./be-prims` | Migrasi tabel, inisialisasi MinIO, dan jalankan server pada port `8080`. |
| `./be-prims seed` | Isi tabel yang sudah ada dengan data demo, lalu keluar. |
| `./be-prims migrate-fresh` | Buat ulang tabel aplikasi dan jalankan seed. |

> [!WARNING]
> `migrate-fresh` menghapus data di tabel aplikasi SQL Server dan tidak menghapus objek di MinIO. Gunakan hanya pada database lokal yang boleh di-reset.

- [`main.go`](main.go), [`routes/`](routes/), [`controllers/`](controllers/): startup, rute API, dan handler HTTP.
- [`database/`](database/), [`models/`](models/), [`request/`](request/): koneksi, migrasi, seed, model, dan validasi input.
- [`middleware/`](middleware/), [`storage/`](storage/), [`audit/`](audit/), [`pdfstamp/`](pdfstamp/): autentikasi dan pembatasan akses, penyimpanan PDF, log akses, dan watermark.

## Pemecahan masalah

| Gejala | Yang perlu diperiksa |
| --- | --- |
| Startup berhenti dengan `env gak ketemu` | Pastikan `.env` ada di direktori kerja saat perintah dijalankan. |
| Gagal terhubung ke SQL Server | Periksa `SQL_SERVER_URL`, status server, dan apakah database `prims` sudah dibuat. |
| Startup berhenti dengan `init storage gagal` | Pastikan MinIO berjalan dan endpoint, kredensial, serta pengaturan TLS sesuai. Jika bucket belum ada, akun MinIO harus boleh membuatnya. |
| Login demo ditolak | Jalankan seed, lalu cocokkan `company_id` dengan kode perusahaan akun dari `GET /company`. |
| PDF contoh tidak ditemukan | Seed hanya membuat metadata. Coba lagi dengan dokumen yang diunggah lewat API. |
