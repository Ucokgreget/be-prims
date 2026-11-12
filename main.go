package main

import (
	"be-prims/audit"
	"be-prims/database"
	"be-prims/routes"
	"be-prims/storage"
	"log"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func main() {
	db := database.Connect()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate-fresh":
			if err := database.MigrateFresh(db); err != nil {
				log.Fatalf("migrate-fresh gagal: %v", err)
			}
			return
		case "seed":
			if err := database.Seed(db); err != nil {
				log.Fatalf("seed gagal: %v", err)
			}
			return
		}
	}

	if err := database.AutoMigrate(db); err != nil {
		log.Fatalf("auto-migration gagal: %v", err)
	}

	if err := storage.Init(); err != nil {
		log.Fatalf("init storage gagal: %v", err)
	}

	router := gin.Default()
	// Tanpa SetTrustedProxies Gin mempercayai SEMUA proxy (0.0.0.0/0), sehingga
	// X-Forwarded-For bisa dipalsukan klien dan ip_address pada audit trail
	// jadi tidak valid. Isi TRUSTED_PROXIES dengan CIDR reverse proxy internal;
	// kosong = pakai IP koneksi langsung (aman & akurat untuk forensik).
	if err := router.SetTrustedProxies(trustedProxies()); err != nil {
		log.Fatalf("konfigurasi TRUSTED_PROXIES tidak valid: %v", err)
	}

	// Satu recorder per proses; worker-nya hidup selama aplikasi berjalan.
	accessLog := audit.NewRecorder(db, audit.Options{})
	routes.SetupRouter(router, db, accessLog)

	router.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "Hello, World!",
		})
	})

	router.Run(":8080")
}

// trustedProxies membaca TRUSTED_PROXIES (dipisah koma). Nilai kosong berarti
// fitur header proxy dimatikan: c.ClientIP() mengembalikan remote address apa
// adanya, yang tidak bisa dipalsukan klien.
func trustedProxies() []string {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	proxies := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			proxies = append(proxies, p)
		}
	}
	return proxies
}
