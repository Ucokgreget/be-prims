package database

import (
	"be-prims/models"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func Seed(db *gorm.DB) error {
	password := "password123"

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password seed: %w", err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		companies := []models.Company{
			{Name: "PT Sri Melamin Rejeki", Code: "SMR", Status: models.StatusActive},
			{Name: "PT Pusri Agro Lestari", Code: "PAL", Status: models.StatusActive},
			{Name: "PT Pusri Investasi Mandiri", Code: "PIM", Status: models.StatusActive},
			{Name: "PT Danendra Niaga Pusri", Code: "DNP", Status: models.StatusActive},
		}
		for i := range companies {
			if err := tx.Where(models.Company{Code: companies[i].Code}).FirstOrCreate(&companies[i]).Error; err != nil {
				return fmt.Errorf("seed company %s: %w", companies[i].Code, err)
			}
		}

		departments := []models.Department{
			{Name: "Teknologi Informasi", Description: "Pengelolaan sistem informasi dan infrastruktur TI.", CompanyID: companies[0].Id},
			{Name: "Sumber Daya Manusia", Description: "Pengelolaan administrasi dan pengembangan karyawan.", CompanyID: companies[0].Id},
			{Name: "Keuangan", Description: "Pengelolaan anggaran dan laporan keuangan.", CompanyID: companies[0].Id},
			{Name: "Operasional", Description: "Pengelolaan kegiatan operasional harian.", CompanyID: companies[1].Id},
		}
		for i := range departments {
			if err := tx.Where(models.Department{Name: departments[i].Name, CompanyID: departments[i].CompanyID}).FirstOrCreate(&departments[i]).Error; err != nil {
				return fmt.Errorf("seed department %s: %w", departments[i].Name, err)
			}
		}

		categories := []models.Category{
			{Name: "SOP", Description: "Standar operasional prosedur perusahaan.", CompanyID: companies[0].Id},
			{Name: "Kebijakan", Description: "Kebijakan dan peraturan internal perusahaan.", CompanyID: companies[0].Id},
			{Name: "Laporan", Description: "Laporan kegiatan dan kinerja perusahaan.", CompanyID: companies[0].Id},
			{Name: "SOP", Description: "Standar operasional prosedur perusahaan.", CompanyID: companies[1].Id},
		}
		for i := range categories {
			if err := tx.Where(models.Category{Name: categories[i].Name, CompanyID: categories[i].CompanyID}).FirstOrCreate(&categories[i]).Error; err != nil {
				return fmt.Errorf("seed category %s: %w", categories[i].Name, err)
			}
		}

		tags := []models.Tag{
			{Name: "Internal", Description: "Dokumen untuk keperluan internal perusahaan."},
			{Name: "Operasional", Description: "Dokumen kegiatan operasional."},
			{Name: "Tahunan", Description: "Dokumen pelaporan tahunan."},
		}
		for i := range tags {
			if err := tx.Where(models.Tag{Name: tags[i].Name}).FirstOrCreate(&tags[i]).Error; err != nil {
				return fmt.Errorf("seed tag %s: %w", tags[i].Name, err)
			}
		}

		users := []models.User{
			{
				Name: "Superadmin Demo", BadgeNumber: "1000", Password: string(hash),
				Jabatan: "Super Administrator Sistem", Role: models.RoleSuperAdmin,
				Status: models.StatusActive, DepartmentID: &departments[0].Id, CompanyID: companies[0].Id,
			},
			{
				Name: "Admin Anper Demo", BadgeNumber: "1001", Password: string(hash),
				Jabatan: "Administrator Anak Perusahaan", Role: models.RoleAdminAnper,
				Status: models.StatusActive, DepartmentID: &departments[0].Id, CompanyID: companies[0].Id,
			},
			{
				Name: "Staf SDM Demo", BadgeNumber: "1002", Password: string(hash),
				Jabatan: "Staf SDM", Role: models.RoleKaryawan,
				Status: models.StatusActive, DepartmentID: &departments[1].Id, CompanyID: companies[0].Id,
			},
			{
				Name: "Staf Keuangan Demo", BadgeNumber: "1003", Password: string(hash),
				Jabatan: "Staf Keuangan", Role: models.RoleKaryawan,
				Status: models.StatusActive, DepartmentID: &departments[2].Id, CompanyID: companies[0].Id,
			},
			{
				Name: "Staf Operasional Demo", BadgeNumber: "2001", Password: string(hash),
				Jabatan: "Staf Operasional", Role: models.RoleKaryawan,
				Status: models.StatusActive, DepartmentID: &departments[3].Id, CompanyID: companies[1].Id,
			},
		}
		for i := range users {
			if err := tx.Omit(clause.Associations).Where(models.User{BadgeNumber: users[i].BadgeNumber}).FirstOrCreate(&users[i]).Error; err != nil {
				return fmt.Errorf("seed user %s: %w", users[i].BadgeNumber, err)
			}
		}

		publicationDate := time.Date(2026, time.January, 15, 8, 0, 0, 0, time.UTC)
		documents := []models.Document{
			{
				UUID: "00000000-0000-4000-8000-000000000001", Title: "Panduan Pengelolaan Arsip Digital",
				Description: "Prosedur penyimpanan dan pencarian dokumen digital.", PublicationDate: publicationDate,
				IsPrivate: false, CategoryID: categories[0].Id,
				CompanyID: companies[0].Id,
			},
			{
				UUID: "00000000-0000-4000-8000-000000000002", Title: "Kebijakan Cuti Karyawan",
				Description: "Ketentuan dan prosedur pengajuan cuti karyawan.", PublicationDate: publicationDate,
				IsPrivate: true, CategoryID: categories[1].Id,
				CompanyID: companies[0].Id,
			},
			{
				UUID: "00000000-0000-4000-8000-000000000003", Title: "Laporan Keuangan Tahunan 2025",
				Description: "Contoh ringkasan laporan keuangan tahun 2025.", PublicationDate: publicationDate,
				IsPrivate: true, CategoryID: categories[2].Id,
				CompanyID: companies[0].Id,
			},
			{
				UUID: "00000000-0000-4000-8000-000000000004", Title: "SOP Operasional PNM",
				Description: "Prosedur operasional harian perusahaan.", PublicationDate: publicationDate,
				IsPrivate: false, CategoryID: categories[3].Id,
				CompanyID: companies[1].Id,
			},
		}
		for i := range documents {
			if err := tx.Omit(clause.Associations).Where(models.Document{UUID: documents[i].UUID}).FirstOrCreate(&documents[i]).Error; err != nil {
				return fmt.Errorf("seed document %s: %w", documents[i].UUID, err)
			}
			if err := tx.Where(models.DocumentDepartment{DocumentID: documents[i].Id}).FirstOrCreate(&models.DocumentDepartment{
				DocumentID: documents[i].Id, DepartmentID: departments[i].Id,
			}).Error; err != nil {
				return fmt.Errorf("seed document-department %s: %w", documents[i].UUID, err)
			}
		}

		// ponytail: attachment hanya metadata contoh; tambahkan fixture PDF jika perlu menguji unduhan.
		attachments := []models.Attachment{
			{DocumentID: documents[0].Id, FileName: "panduan-arsip-digital.pdf", FilePath: "fixtures/seed/panduan-arsip-digital.pdf"},
			{DocumentID: documents[1].Id, FileName: "kebijakan-cuti.pdf", FilePath: "fixtures/seed/kebijakan-cuti.pdf"},
			{DocumentID: documents[2].Id, FileName: "laporan-keuangan-2025.pdf", FilePath: "fixtures/seed/laporan-keuangan-2025.pdf"},
			{DocumentID: documents[3].Id, FileName: "sop-operasional-pnm.pdf", FilePath: "fixtures/seed/sop-operasional-pnm.pdf"},
		}
		for i := range attachments {
			key := models.Attachment{DocumentID: attachments[i].DocumentID, FileName: attachments[i].FileName}
			if err := tx.Omit(clause.Associations).Where(key).FirstOrCreate(&attachments[i]).Error; err != nil {
				return fmt.Errorf("seed attachment %s: %w", attachments[i].FileName, err)
			}
		}

		documentTags := []models.DocumentTag{
			{DocumentID: documents[0].Id, TagID: tags[1].Id},
			{DocumentID: documents[1].Id, TagID: tags[0].Id},
			{DocumentID: documents[2].Id, TagID: tags[2].Id},
			{DocumentID: documents[3].Id, TagID: tags[1].Id},
		}
		for i := range documentTags {
			if err := tx.Where(documentTags[i]).FirstOrCreate(&documentTags[i]).Error; err != nil {
				return fmt.Errorf("seed document tag: %w", err)
			}
		}

		shares := []models.DocumentShared{
			{DocumentID: documents[1].Id, UserID: users[2].Id, SharedBy: users[1].Id, SharedAt: publicationDate.Add(time.Hour)},
			{DocumentID: documents[2].Id, UserID: users[3].Id, SharedBy: users[1].Id, SharedAt: publicationDate.Add(time.Hour)},
		}
		for i := range shares {
			key := models.DocumentShared{DocumentID: shares[i].DocumentID, UserID: shares[i].UserID}
			if err := tx.Omit(clause.Associations).Where(key).FirstOrCreate(&shares[i]).Error; err != nil {
				return fmt.Errorf("seed document shared: %w", err)
			}
		}

		accessLogs := []models.DocumentAccessLog{
			{DocumentID: documents[0].Id, UserID: users[0].Id, Action: "VIEW", AccessedAt: publicationDate.Add(2 * time.Hour), IpAddress: "127.0.0.1"},
			{DocumentID: documents[1].Id, UserID: users[2].Id, Action: "VIEW", AccessedAt: publicationDate.Add(3 * time.Hour), IpAddress: "127.0.0.1"},
			{DocumentID: documents[2].Id, UserID: users[3].Id, Action: "DOWNLOAD", AccessedAt: publicationDate.Add(4 * time.Hour), IpAddress: "127.0.0.1"},
		}
		for i := range accessLogs {
			if err := tx.Where(accessLogs[i]).FirstOrCreate(&accessLogs[i]).Error; err != nil {
				return fmt.Errorf("seed document access log: %w", err)
			}
		}

		return nil
	})
}
