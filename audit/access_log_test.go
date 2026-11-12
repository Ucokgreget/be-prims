package audit

import (
	"errors"
	"testing"
	"time"

	"be-prims/models"

	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newAuditTestDB: DryRun SQL Server, pola sama dengan test controller. Insert
// tidak benar-benar dieksekusi; callback Create dipakai untuk menghitung baris
// yang akan ditulis.
func newAuditTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlserver.Open("sqlserver://localhost?database=audit_test"), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// countCreates menghitung percobaan INSERT ke document_access_logs.
func countCreates(t *testing.T, db *gorm.DB, failWith error) *int {
	t.Helper()
	count := new(int)
	if err := db.Callback().Create().Before("gorm:create").Register("test:count", func(tx *gorm.DB) {
		if tx.Statement.Table != "document_access_logs" {
			return
		}
		*count++
		if failWith != nil {
			tx.AddError(failWith)
		}
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

// dryRunPluck kosong pada DryRun, jadi cek cooldown di DB tidak pernah
// menemukan baris lama. Test yang butuh perilaku lintas-replika disimulasikan
// lewat fakeLastRow.
func fakeLastRow(t *testing.T, db *gorm.DB, last *time.Time) {
	t.Helper()
	if err := db.Callback().Query().After("gorm:query").Register("test:last-row", func(tx *gorm.DB) {
		if tx.Statement.Table != "document_access_logs" {
			return
		}
		if dest, ok := tx.Statement.Dest.(*time.Time); ok && last != nil {
			*dest = *last
		}
		tx.RowsAffected = 1
	}); err != nil {
		t.Fatal(err)
	}
}

func entry(docID, userID uint, at time.Time) models.DocumentAccessLog {
	return models.DocumentAccessLog{
		DocumentID: docID, UserID: userID, Action: ActionView,
		AccessedAt: at, IpAddress: "10.14.22.105",
	}
}

func TestRecorderDebouncesWithinCooldown(t *testing.T) {
	db := newAuditTestDB(t)
	count := countCreates(t, db, nil)

	base := time.Date(2026, 11, 12, 0, 10, 0, 0, time.UTC)
	clock := base
	r := NewRecorder(db, Options{Cooldown: time.Minute, Now: func() time.Time { return clock }})
	t.Cleanup(r.Close)

	r.Record(entry(15, 3, r.now()))
	clock = base.Add(20 * time.Second)
	r.Record(entry(15, 3, r.now())) // refresh viewer < 1 menit: harus di-skip
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 1 {
		t.Fatalf("insert = %d, want 1 (debounce 1 menit)", *count)
	}

	// Lewat cooldown: aksi berikutnya dicatat lagi.
	clock = base.Add(2 * time.Minute)
	r.Record(entry(15, 3, r.now()))
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 2 {
		t.Fatalf("insert = %d, want 2 setelah cooldown lewat", *count)
	}
}

func TestRecorderSeparatesKeys(t *testing.T) {
	db := newAuditTestDB(t)
	count := countCreates(t, db, nil)

	now := time.Date(2026, 11, 12, 0, 10, 0, 0, time.UTC)
	r := NewRecorder(db, Options{Now: func() time.Time { return now }})
	t.Cleanup(r.Close)

	r.Record(entry(15, 3, r.now())) // user 3
	r.Record(entry(15, 4, r.now())) // user lain
	r.Record(entry(16, 3, r.now())) // dokumen lain
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 3 {
		t.Fatalf("insert = %d, want 3 (key = dokumen+user+action)", *count)
	}
}

func TestRecorderCooldownFromDatabase(t *testing.T) {
	db := newAuditTestDB(t)
	count := countCreates(t, db, nil)

	now := time.Date(2026, 11, 12, 0, 10, 0, 0, time.UTC)
	// Baris lama 30 detik sebelumnya (mis. dari replika lain): gate in-memory
	// kosong, jadi keputusan harus datang dari cek DB.
	last := now.Add(-30 * time.Second)
	fakeLastRow(t, db, &last)

	r := NewRecorder(db, Options{Now: func() time.Time { return now }})
	t.Cleanup(r.Close)

	r.Record(entry(15, 3, now))
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 0 {
		t.Fatalf("insert = %d, want 0 (baris < 1 menit sudah ada di DB)", *count)
	}
}

func TestRecorderRetriesAfterWriteFailure(t *testing.T) {
	db := newAuditTestDB(t)
	count := countCreates(t, db, errors.New("db down"))

	now := time.Date(2026, 11, 12, 0, 10, 0, 0, time.UTC)
	r := NewRecorder(db, Options{Now: func() time.Time { return now }})
	t.Cleanup(r.Close)

	r.Record(entry(15, 3, now))
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 1 {
		t.Fatalf("insert attempt = %d, want 1", *count)
	}

	// Claim harus dilepas: kegagalan tulis tidak boleh menahan aksi berikutnya
	// selama masa cooldown.
	r.Record(entry(15, 3, now.Add(time.Second)))
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 2 {
		t.Fatalf("insert attempt = %d, want 2 (claim dilepas setelah gagal)", *count)
	}
}

// Record tidak boleh memblokir pemanggil walau antrean penuh dan worker macet.
func TestRecorderNeverBlocksWhenQueueFull(t *testing.T) {
	db := newAuditTestDB(t)

	// Worker tidak pernah dijalankan: semua kapasitas antrean tetap terpakai.
	r := &Recorder{
		db:       db,
		cooldown: time.Minute,
		now:      time.Now,
		queue:    make(chan models.DocumentAccessLog, 1),
		recent:   make(map[logKey]time.Time),
		done:     make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Record(entry(15, 3, time.Now())) // mengisi antrean
		r.Record(entry(15, 4, time.Now())) // antrean penuh: harus langsung kembali
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record memblokir saat antrean penuh")
	}
}

func TestRecorderIgnoresIncompleteEntry(t *testing.T) {
	db := newAuditTestDB(t)
	count := countCreates(t, db, nil)

	r := NewRecorder(db, Options{})
	t.Cleanup(r.Close)

	r.Record(models.DocumentAccessLog{DocumentID: 15, Action: ActionView}) // tanpa user
	r.Record(models.DocumentAccessLog{UserID: 3, Action: ActionView})      // tanpa dokumen
	r.Record(models.DocumentAccessLog{DocumentID: 15, UserID: 3})          // tanpa action
	if !r.Flush(2 * time.Second) {
		t.Fatal("flush timeout")
	}
	if *count != 0 {
		t.Fatalf("insert = %d, want 0 untuk entri tidak lengkap", *count)
	}
}

// Recorder nil (mis. wiring lama tanpa audit) tidak boleh panic.
func TestNilRecorderSafe(t *testing.T) {
	var r *Recorder
	r.Record(entry(1, 1, time.Now()))
	r.Close()
	if !r.Flush(time.Second) {
		t.Fatal("Flush pada nil recorder harus true")
	}
}
