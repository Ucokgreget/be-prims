// Package audit mencatat jejak akses dokumen (forensic audit trail) ke tabel
// document_access_logs untuk kebutuhan US-10-BE.
//
// Dua sifat yang dijaga di sini:
//
//   - Non-blocking: handler tidak pernah menunggu INSERT. Entri dikirim ke
//     buffered channel; bila antrean penuh entri dibuang dan dicatat ke log,
//     bukan memperlambat atau menggagalkan pengaliran PDF.
//   - Terisolasi: semua kegagalan tulis hanya di-log. Tidak ada error yang
//     merambat ke response HTTP.
//
// Cooldown dipasang dua lapis (in-memory lalu DB) supaya refresh viewer
// berulang tidak membanjiri tabel, termasuk saat aplikasi berjalan lebih dari
// satu replika:
//
//   - Gate in-memory: murah dan menyerap spam refresh pada proses yang sama.
//   - Cek DB di worker: benar lintas replika/restart; query memakai index
//     komposit idx_access_log_lookup.
package audit

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"be-prims/models"

	"gorm.io/gorm"
)

// ActionView adalah nilai kolom action untuk aktivitas baca dokumen.
const ActionView = "VIEW"

const (
	// DefaultCooldown: aksi yang sama oleh user yang sama pada dokumen yang
	// sama dalam rentang ini hanya dicatat sekali.
	DefaultCooldown = time.Minute
	// DefaultQueueSize: kapasitas antrean tulis sebelum entri mulai dibuang.
	DefaultQueueSize = 512
	// pruneThreshold: jumlah maksimum key cooldown di memori sebelum di-prune.
	pruneThreshold = 1 << 14
)

// Options mengatur perilaku recorder. Zero value memakai default.
type Options struct {
	Cooldown  time.Duration
	QueueSize int
	// Now dapat diganti pada test; default time.Now.
	Now func() time.Time
}

type logKey struct {
	DocumentID uint
	UserID     uint
	Action     string
}

func keyOf(entry models.DocumentAccessLog) logKey {
	return logKey{DocumentID: entry.DocumentID, UserID: entry.UserID, Action: entry.Action}
}

// Recorder menulis document_access_logs secara asinkron.
//
// Recorder aman dipakai bersamaan oleh banyak goroutine handler. Buat satu
// instance per *gorm.DB saat wiring route, jangan per request.
type Recorder struct {
	db       *gorm.DB
	cooldown time.Duration
	now      func() time.Time
	queue    chan models.DocumentAccessLog

	mu     sync.Mutex
	recent map[logKey]time.Time

	// pending: jumlah entri yang sudah diterima tapi belum selesai diproses.
	// Dipakai Flush() agar test/shutdown bisa menunggu tanpa memblokir Record.
	pending atomic.Int64
	closed  atomic.Bool

	closeOnce sync.Once
	done      chan struct{}
}

// NewRecorder membuat Recorder sekaligus menjalankan worker-nya.
func NewRecorder(db *gorm.DB, opts Options) *Recorder {
	if opts.Cooldown <= 0 {
		opts.Cooldown = DefaultCooldown
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = DefaultQueueSize
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	r := &Recorder{
		db:       db,
		cooldown: opts.Cooldown,
		now:      opts.Now,
		queue:    make(chan models.DocumentAccessLog, opts.QueueSize),
		recent:   make(map[logKey]time.Time),
		done:     make(chan struct{}),
	}
	go r.worker()
	return r
}

// Record menjadwalkan penyimpanan satu entri audit tanpa memblokir pemanggil.
//
// AccessedAt diisi waktu server saat aksi terjadi (bukan saat worker kebetulan
// memproses), sehingga timestamp tetap akurat meski antrean sedang ramai.
func (r *Recorder) Record(entry models.DocumentAccessLog) {
	if r == nil || r.db == nil {
		return
	}
	if entry.Action == "" || entry.DocumentID == 0 || entry.UserID == 0 {
		log.Printf("audit: entri tidak lengkap diabaikan: %+v", entry)
		return
	}
	if entry.AccessedAt.IsZero() {
		entry.AccessedAt = r.now()
	}
	if r.closed.Load() {
		return // proses sedang shutdown; jangan kirim ke worker yang berhenti
	}

	if !r.claim(entry) {
		return
	}

	// pending dinaikkan SEBELUM kirim: kalau worker sudah selesai sebelum
	// penghitung sempat naik, Flush bisa melihat 0 saat entri masih diproses.
	r.pending.Add(1)
	select {
	case r.queue <- entry:
	default:
		r.pending.Add(-1)
		// Antrean penuh: jangan blokir stream. Buka lagi gate supaya aksi
		// berikutnya masih bisa tercatat.
		r.release(entry)
		log.Printf("audit: antrean penuh, entri %s doc=%d user=%d dibuang",
			entry.Action, entry.DocumentID, entry.UserID)
	}
}

// Flush menunggu seluruh entri yang sudah dijadwalkan selesai diproses.
// Mengembalikan false bila timeout terlampaui. Dipakai test dan shutdown;
// tidak perlu dipanggil dari handler.
func (r *Recorder) Flush(timeout time.Duration) bool {
	if r == nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	for {
		if r.pending.Load() == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// Close berhenti menerima entri baru lalu menunggu antrean terkuras (dibatasi
// 5 detik) sebelum mematikan worker. Dipakai teardown test dan shutdown.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.closed.Store(true)
		if !r.Flush(5 * time.Second) {
			log.Printf("audit: shutdown tanpa menunggu seluruh antrean selesai")
		}
		close(r.done)
	})
}

// claim menandai pasangan (document, user, action) sebagai baru dicatat.
// Mengembalikan false bila masih dalam masa cooldown.
func (r *Recorder) claim(entry models.DocumentAccessLog) bool {
	key := keyOf(entry)
	now := entry.AccessedAt

	r.mu.Lock()
	defer r.mu.Unlock()

	if last, ok := r.recent[key]; ok && now.Sub(last) < r.cooldown {
		return false
	}
	r.recent[key] = now
	// Prune entri kedaluwarsa supaya map tidak tumbuh tanpa batas selama
	// proses hidup (jumlah pasangan dokumen x user bisa sangat besar).
	if len(r.recent) > pruneThreshold {
		cutoff := now.Add(-r.cooldown)
		for k, at := range r.recent {
			if at.Before(cutoff) {
				delete(r.recent, k)
			}
		}
	}
	return true
}

// release membatalkan claim agar aksi berikutnya tidak ikut tertahan cooldown
// padahal barisnya tidak pernah tersimpan. Hanya menghapus bila timestamp yang
// tersimpan memang milik entri ini: entri lain yang lebih baru tidak boleh
// ikut terhapus.
func (r *Recorder) release(entry models.DocumentAccessLog) {
	key := keyOf(entry)
	r.mu.Lock()
	defer r.mu.Unlock()
	if at, ok := r.recent[key]; ok && at.Equal(entry.AccessedAt) {
		delete(r.recent, key)
	}
}

func (r *Recorder) worker() {
	for {
		select {
		case <-r.done:
			return
		case entry := <-r.queue:
			r.persist(entry)
		}
	}
}

func (r *Recorder) persist(entry models.DocumentAccessLog) {
	defer r.pending.Add(-1)

	recent, err := r.hasRecentRow(entry)
	if err != nil {
		// Gagal cek cooldown bukan alasan membuang bukti audit: lanjut simpan.
		log.Printf("audit: gagal cek cooldown doc=%d user=%d: %v", entry.DocumentID, entry.UserID, err)
	} else if recent {
		return
	}

	if err := r.db.Create(&entry).Error; err != nil {
		log.Printf("audit: gagal mencatat %s doc=%d user=%d: %v",
			entry.Action, entry.DocumentID, entry.UserID, err)
		r.release(entry)
	}
}

// hasRecentRow menanyakan DB apakah sudah ada entri serupa dalam masa cooldown.
// Ini menutup celah gate in-memory yang hanya berlaku per proses.
func (r *Recorder) hasRecentRow(entry models.DocumentAccessLog) (bool, error) {
	var last time.Time
	if err := r.db.Model(&models.DocumentAccessLog{}).
		Where("document_id = ? AND user_id = ? AND action = ?",
			entry.DocumentID, entry.UserID, entry.Action).
		Order("accessed_at DESC").
		Limit(1).
		Pluck("accessed_at", &last).Error; err != nil {
		return false, err
	}
	if last.IsZero() {
		return false, nil
	}
	return entry.AccessedAt.Sub(last) < r.cooldown, nil
}
