# Panduan Backup & Restore Data Tenant dari TimescaleDB

Dokumen ini menjelaskan prosedur untuk melakukan **backup dan restore data-only** per-tenant pada database TimescaleDB `atlas-api` menggunakan utility psql `\copy`.

---

## Mengapa Menggunakan `\copy` dengan Kolom Eksplisit?

1. **Keamanan Alur Ingest TimescaleDB**: Dengan meng-import data melalui tabel induk hypertable (bukan menyalin tabel chunk fisik), TimescaleDB di target akan mengarahkan baris data ke chunk yang sesuai secara otomatis. Ini menghindari terjadinya **orphan/shadow chunk** akibat perbedaan metadata catalog TimescaleDB antara source dan target.
2. **Kompabilitas Struktur Kolom**: Urutan kolom di database source dan target bisa saja berbeda meskipun nama kolomnya sama. Mencantumkan daftar kolom secara tegas saat backup dan restore menjamin data terpetakan dengan benar ke kolom yang tepat.
3. **Penanganan Teks Multiline & Escape Karakter**: Teks yang memuat karakter khusus, tanda kutip, JSON (`payload_json`, `metadata`), atau baris baru (*multiline*) akan dibungkus dengan aman menggunakan standar CSV PostgreSQL.

---

## Urutan Eksekusi Restore (Topological Order)

Karena terdapat relasi *Foreign Key* antar tabel, proses restore harus dilakukan secara berurutan sesuai urutan dependensi tabel berikut:

1. `email`
2. `product`
3. `product_variant`
4. `platform_product`
5. `account`
6. `account_profile`
7. `account_user`
8. `account_modifier`
9. `transaction_ts` (Hypertable)
10. `transaction_item_ts` (Hypertable)
11. `expense` (Hypertable)
12. `email_subject`
13. `email_message_ts` (Hypertable)

---

## 1. Prosedur Backup (Export)

Jalankan perintah `\copy` melalui `psql` shell pada database source. Ganti `<tenant_id>` dengan nama schema tenant yang ingin di-backup (misalnya `paytronik`).

Gunakan opsi `FORCE QUOTE *` untuk memaksa semua nilai dibungkus tanda kutip guna mencegah kerusakan data akibat karakter *newline* atau karakter pemisah lainnya dalam kolom bertipe teks/JSON.

### Contoh Command Export:

```sql
-- 1. email
\copy (SELECT id, email, password, created_at, updated_at FROM "<tenant_id>".email) TO 'backup_email.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 2. product
\copy (SELECT id, name, created_at, updated_at FROM "<tenant_id>".product) TO 'backup_product.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 3. product_variant
\copy (SELECT id, name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at FROM "<tenant_id>".product_variant) TO 'backup_product_variant.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 4. platform_product
\copy (SELECT id, platform, name, variant, product_variant_id, created_at, updated_at FROM "<tenant_id>".platform_product) TO 'backup_platform_product.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 5. account
\copy (SELECT id, account_password, subscription_expiry, status, billing, batch_start_date, batch_end_date, email_id, product_variant_id, label, freeze_until, pinned, created_at, updated_at FROM "<tenant_id>".account) TO 'backup_account.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 6. account_profile
\copy (SELECT id, name, max_user, allow_generate, metadata, account_id, created_at, updated_at FROM "<tenant_id>".account_profile) TO 'backup_account_profile.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 7. account_user
\copy (SELECT id, name, status, account_id, account_profile_id, expired_at, created_at, updated_at FROM "<tenant_id>".account_user) TO 'backup_account_user.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 8. account_modifier
\copy (SELECT id, modifier_id, account_id, enabled, metadata, created_at, updated_at FROM "<tenant_id>".account_modifier) TO 'backup_account_modifier.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 9. transaction_ts (Hypertable)
\copy (SELECT id, customer, platform, total_price, created_at FROM "<tenant_id>".transaction_ts) TO 'backup_transaction_ts.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 10. transaction_item_ts (Hypertable)
\copy (SELECT id, transaction_id, price, account_id, account_user_id, product_id, product_variant_id, created_at FROM "<tenant_id>".transaction_item_ts) TO 'backup_transaction_item_ts.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 11. expense (Hypertable)
\copy (SELECT id, subject_id, type, amount, note, created_at FROM "<tenant_id>".expense) TO 'backup_expense.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 12. email_subject
\copy (SELECT id, context, subject, extract_method, created_at, updated_at FROM "<tenant_id>".email_subject) TO 'backup_email_subject.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;

-- 13. email_message_ts (Hypertable)
\copy (SELECT id, tenant_id, from_email, subject, email_date, parsed_context, parsed_data, created_at FROM "<tenant_id>".email_message_ts) TO 'backup_email_message_ts.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"' FORCE QUOTE *;
```

---

## 2. Prosedur Restore (Import Data-Only)

Pastikan migrasi skema database (`up`) sudah selesai dijalankan di database target sehingga skema per-tenant sudah terbentuk lengkap dengan semua trigger, indeks, hypertable, dan continuous aggregate.

Saat melakukan restore, kolom dicantumkan secara eksplisit sesuai urutan yang ada di file CSV hasil backup.

### Contoh Command Import:

```sql
-- 1. email
\copy "<tenant_id>".email (id, email, password, created_at, updated_at) FROM 'backup_email.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 2. product
\copy "<tenant_id>".product (id, name, created_at, updated_at) FROM 'backup_product.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 3. product_variant
\copy "<tenant_id>".product_variant (id, name, duration, interval, cooldown, copy_template, base_price, product_id, created_at, updated_at) FROM 'backup_product_variant.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 4. platform_product
\copy "<tenant_id>".platform_product (id, platform, name, variant, product_variant_id, created_at, updated_at) FROM 'backup_platform_product.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 5. account
\copy "<tenant_id>".account (id, account_password, subscription_expiry, status, billing, batch_start_date, batch_end_date, email_id, product_variant_id, label, freeze_until, pinned, created_at, updated_at) FROM 'backup_account.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 6. account_profile
\copy "<tenant_id>".account_profile (id, name, max_user, allow_generate, metadata, account_id, created_at, updated_at) FROM 'backup_account_profile.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 7. account_user
\copy "<tenant_id>".account_user (id, name, status, account_id, account_profile_id, expired_at, created_at, updated_at) FROM 'backup_account_user.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 8. account_modifier
\copy "<tenant_id>".account_modifier (id, modifier_id, account_id, enabled, metadata, created_at, updated_at) FROM 'backup_account_modifier.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 9. transaction_ts (Hypertable - akan otomatis di-route ke chunks target secara aman)
\copy "<tenant_id>".transaction_ts (id, customer, platform, total_price, created_at) FROM 'backup_transaction_ts.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 10. transaction_item_ts (Hypertable)
\copy "<tenant_id>".transaction_item_ts (id, transaction_id, price, account_id, account_user_id, product_id, product_variant_id, created_at) FROM 'backup_transaction_item_ts.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 11. expense (Hypertable)
\copy "<tenant_id>".expense (id, subject_id, type, amount, note, created_at) FROM 'backup_expense.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 12. email_subject
\copy "<tenant_id>".email_subject (id, context, subject, extract_method, created_at, updated_at) FROM 'backup_email_subject.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';

-- 13. email_message_ts (Hypertable)
\copy "<tenant_id>".email_message_ts (id, tenant_id, from_email, subject, email_date, parsed_context, parsed_data, created_at) FROM 'backup_email_message_ts.csv' WITH CSV HEADER ESCAPE '"' QUOTE '"';
```

---

## 3. Langkah Penting Setelah Restore: Sinkronisasi Sekuens Kolom Identitas

Karena data dimasukkan beserta ID eksplisitnya, database tidak memajukan nilai generator sekuens otomatis kolom `GENERATED BY DEFAULT AS IDENTITY`. Anda harus mensinkronkan ulang sekuensnya agar tidak terjadi tabrakan ID baru saat aplikasi berjalan:

Jalankan perintah SQL berikut setelah selesai memulihkan seluruh tabel:

```sql
SELECT setval(pg_get_serial_sequence('"<tenant_id>".email', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".email;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".product', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".product;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".product_variant', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".product_variant;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".platform_product', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".platform_product;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".account', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".account;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".account_profile', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".account_profile;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".account_user', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".account_user;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".account_modifier', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".account_modifier;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".transaction_item_ts', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".transaction_item_ts;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".expense', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".expense;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".email_subject', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".email_subject;
SELECT setval(pg_get_serial_sequence('"<tenant_id>".email_message_ts', 'id'), COALESCE(MAX(id), 0) + 1, false) FROM "<tenant_id>".email_message_ts;
```
